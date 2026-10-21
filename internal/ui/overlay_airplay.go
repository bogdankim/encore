package ui

import (
	"context"
	"errors"
	"image"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
)

const (
	airplayW      = 50
	airplayMeterW = 10
	airplayStep   = 5
	// airplayGrace keeps an optimistic change on screen after its call
	// returns, because Music can report the old value for about a second.
	airplayGrace = 1500 * time.Millisecond
)

// airplayListMsg is a fresh device list for one picker.
type airplayListMsg struct {
	owner *AirPlayOverlay
	devs  []bridge.AirPlayDevice
	err   error
}

// airplayDoneMsg reports a select (pid "") or volume call.
type airplayDoneMsg struct {
	owner *AirPlayOverlay
	pid   string
	name  string
	devs  []bridge.AirPlayDevice // set when the call had to re-list
	err   error
}

// airplaySender serialises bridge calls so the newest wanted state is what
// Music ends up with, however the commands are scheduled. Commands take the
// latest value when they run; a command that finds nothing left is a no-op.
type airplaySender struct {
	call sync.Mutex
	mu   sync.Mutex
	sel  []string
	vols map[string]int
}

func (s *airplaySender) wantSel(pids []string) {
	s.mu.Lock()
	s.sel = slices.Clone(pids)
	s.mu.Unlock()
}

func (s *airplaySender) takeSel() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.sel
	s.sel = nil
	return p
}

func (s *airplaySender) wantVol(pid string, v int) {
	s.mu.Lock()
	s.vols[pid] = v
	s.mu.Unlock()
}

func (s *airplaySender) takeVol(pid string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vols[pid]
	delete(s.vols, pid)
	return v, ok
}

// airplayPending is an optimistic change shown until the bridge confirms it.
type airplayPending struct {
	inflight int
	until    time.Time
}

func (p airplayPending) live(now time.Time) bool { return p.inflight > 0 || now.Before(p.until) }

// AirPlayOverlay picks AirPlay outputs: several can play at once, each with
// its own volume.
//
// The rows work like checkboxes: clicking a device (or space) adds it to or
// removes it from the outputs, and the last output can't be removed. Enter
// or a right-click plays on that device only. The meter on a playing device
// sets its volume by click, drag or wheel, or h/l and ←/→ in steps of 5.
// These are the per-device volumes; Music's own volume is the one in the
// player bar, so the meters get a "Volume" heading.
type AirPlayOverlay struct {
	client  bridge.Client
	send    *airplaySender
	started bool
	loaded  bool
	real    []bridge.AirPlayDevice // as the bridge last reported them
	devs    []bridge.AirPlayDevice // real with pending changes on top
	wantSel []string
	sel     airplayPending
	wantVol map[string]int
	vol     map[string]airplayPending

	cur   ovCursor
	focus string // pid of the focused row, kept across list updates
	drag  string // pid whose meter is being dragged
	note  string // one dim line at the bottom: the last failure

	w, h, listY, listH int
}

// NewAirPlay opens the AirPlay picker. It asks the bridge for devices when
// it is first initialised or updated.
func NewAirPlay(s Services) Overlay {
	return &AirPlayOverlay{
		client:  s.Client,
		send:    &airplaySender{vols: map[string]int{}},
		wantVol: map[string]int{},
		vol:     map[string]airplayPending{},
		cur:     ovCursor{Hover: -1},
	}
}

// Init lists the devices. The bridge answers from its cache at once and
// sends an AirPlayEvent shortly after if anything changed.
func (o *AirPlayOverlay) Init(c *Ctx) tea.Cmd {
	if o.started || o.client == nil {
		return nil
	}
	o.started = true
	return o.list()
}

func (o *AirPlayOverlay) list() tea.Cmd {
	client := o.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), bridge.DefaultTimeout)
		defer cancel()
		devs, err := client.AirPlayDevices(ctx)
		return airplayListMsg{owner: o, devs: devs, err: err}
	}
}

func (o *AirPlayOverlay) Rect(screenW, screenH int) image.Rectangle {
	w := min(airplayW, screenW)
	top := 4
	if screenH < 20 {
		top = 2 // the player bar is one line
	}
	h := 4 + o.headH() + max(1, len(o.devs))
	if screenH-top < h {
		top = max(0, screenH-h)
	}
	h = min(h, screenH-top)
	x := max(0, screenW-w-1)
	o.w, o.h = w, h
	return image.Rect(x, top, x+w, top+h)
}

func (o *AirPlayOverlay) CapturesInput() bool { return true }

// headH is 1 when a meter shows and the "Volume" heading goes above it.
func (o *AirPlayOverlay) headH() int {
	for _, d := range o.devs {
		if d.Selected && d.Available {
			return 1
		}
	}
	return 0
}

func (o *AirPlayOverlay) ok(i int) bool { return i >= 0 && i < len(o.devs) && o.devs[i].Available }

func (o *AirPlayOverlay) Update(msg tea.Msg, c *Ctx) tea.Cmd {
	var init tea.Cmd
	if !o.started {
		init = o.Init(c)
	}
	switch m := msg.(type) {
	case airplayListMsg:
		if m.owner != o {
			break
		}
		if m.err != nil {
			o.loaded = true
			o.note = airplayErrText(m.err, "")
			break
		}
		o.setReal(m.devs, c.Now)
	case bridge.AirPlayEvent:
		o.setReal(m.Devices, c.Now)
	case BridgeMsg:
		if ev, ok := m.Event.(bridge.AirPlayEvent); ok {
			o.setReal(ev.Devices, c.Now)
		}
	case airplayDoneMsg:
		if m.owner != o {
			break
		}
		return tea.Batch(init, o.done(m, c.Now))
	}
	return init
}

func (o *AirPlayOverlay) done(m airplayDoneMsg, now time.Time) tea.Cmd {
	p := o.pending(m.pid)
	p.inflight = max(0, p.inflight-1)
	if p.inflight == 0 {
		p.until = now.Add(airplayGrace)
	}
	if m.err != nil {
		o.note = airplayErrText(m.err, m.name)
		p = airplayPending{}
		if m.pid == "" {
			o.wantSel = nil
		} else {
			delete(o.wantVol, m.pid)
		}
	}
	o.setPending(m.pid, p)
	switch {
	case m.err != nil:
		o.apply(now)
		return o.list()
	case m.devs != nil:
		o.setReal(m.devs, now)
	}
	return nil
}

// pending is the optimistic state of the selection (pid "") or of one
// device's volume.
func (o *AirPlayOverlay) pending(pid string) airplayPending {
	if pid == "" {
		return o.sel
	}
	return o.vol[pid]
}

func (o *AirPlayOverlay) setPending(pid string, p airplayPending) {
	if pid == "" {
		o.sel = p
	} else {
		o.vol[pid] = p
	}
}

// setReal takes a list from the bridge and re-applies what's still pending.
func (o *AirPlayOverlay) setReal(devs []bridge.AirPlayDevice, now time.Time) {
	o.real = slices.Clone(devs)
	o.loaded = true
	o.apply(now)
}

// apply rebuilds the shown list from real plus pending changes and keeps
// the cursor on the same device.
func (o *AirPlayOverlay) apply(now time.Time) {
	devs := slices.Clone(o.real)
	if o.wantSel != nil && o.sel.live(now) {
		for i := range devs {
			devs[i].Selected = slices.Contains(o.wantSel, devs[i].PID)
		}
	}
	for i := range devs {
		if v, ok := o.wantVol[devs[i].PID]; ok && o.vol[devs[i].PID].live(now) {
			devs[i].Volume = v
		}
	}
	o.devs = devs
	n := len(devs)
	if i := slices.IndexFunc(devs, func(d bridge.AirPlayDevice) bool { return d.PID == o.focus }); i >= 0 && o.ok(i) {
		o.cur.Sel = i
	} else {
		o.cur.Sel = slices.IndexFunc(devs, func(d bridge.AirPlayDevice) bool { return d.Selected && d.Available })
		if o.cur.Sel < 0 {
			o.cur.First(n, o.ok)
		}
	}
	if o.cur.Hover >= n {
		o.cur.Hover = -1
	}
	o.syncFocus()
	o.cur.Follow(o.listH, n, o.ok)
}

func (o *AirPlayOverlay) syncFocus() {
	if o.ok(o.cur.Sel) {
		o.focus = o.devs[o.cur.Sel].PID
	}
}

func (o *AirPlayOverlay) selectedPIDs() []string {
	var out []string
	for _, d := range o.devs {
		if d.Selected {
			out = append(out, d.PID)
		}
	}
	return out
}

// only plays on device i alone.
func (o *AirPlayOverlay) only(i int, c *Ctx) tea.Cmd {
	if !o.ok(i) {
		return nil
	}
	pids := o.selectedPIDs()
	if len(pids) == 1 && pids[0] == o.devs[i].PID {
		return nil
	}
	return o.choose([]string{o.devs[i].PID}, o.devs[i].Name, c.Now)
}

// toggle adds device i to the outputs or removes it. The last output stays.
func (o *AirPlayOverlay) toggle(i int, c *Ctx) tea.Cmd {
	if !o.ok(i) {
		return nil
	}
	d := o.devs[i]
	pids := o.selectedPIDs()
	if d.Selected {
		if len(pids) == 1 {
			o.note = "Music needs at least one output"
			return nil
		}
		pids = slices.DeleteFunc(pids, func(p string) bool { return p == d.PID })
	} else {
		pids = append(pids, d.PID)
	}
	return o.choose(pids, d.Name, c.Now)
}

func (o *AirPlayOverlay) choose(pids []string, name string, now time.Time) tea.Cmd {
	o.note = ""
	o.wantSel = pids
	o.sel.inflight++
	o.apply(now)
	o.send.wantSel(pids)
	client, send, snap := o.client, o.send, slices.Clone(o.real)
	return func() tea.Msg {
		send.call.Lock()
		defer send.call.Unlock()
		msg := airplayDoneMsg{owner: o, name: name}
		pids := send.takeSel()
		if pids == nil || client == nil {
			return msg
		}
		msg.devs, msg.err = airplayCall(client, snap, pids, func(ctx context.Context, ids []int) error {
			return client.SelectAirPlay(ctx, ids)
		})
		return msg
	}
}

// setVolume changes device i's volume on screen; send makes the bridge call
// too (a drag sends once, on release).
func (o *AirPlayOverlay) setVolume(i, v int, send bool, c *Ctx) tea.Cmd {
	if i < 0 || i >= len(o.devs) || !o.devs[i].Selected || !o.devs[i].Available {
		return nil
	}
	d := o.devs[i]
	v = min(max(v, 0), 100)
	o.note = ""
	o.wantVol[d.PID] = v
	p := o.vol[d.PID]
	if !send {
		p.until = c.Now.Add(time.Hour) // held by the drag
		o.vol[d.PID] = p
		o.apply(c.Now)
		return nil
	}
	p.inflight++
	o.vol[d.PID] = p
	o.apply(c.Now)
	o.send.wantVol(d.PID, v)
	client, send2, snap := o.client, o.send, slices.Clone(o.real)
	return func() tea.Msg {
		send2.call.Lock()
		defer send2.call.Unlock()
		msg := airplayDoneMsg{owner: o, pid: d.PID, name: d.Name}
		v, ok := send2.takeVol(d.PID)
		if !ok || client == nil {
			return msg
		}
		msg.devs, msg.err = airplayCall(client, snap, []string{d.PID}, func(ctx context.Context, ids []int) error {
			return client.SetAirPlayVolume(ctx, ids[0], v)
		})
		return msg
	}
}

// airplayCall maps pids to the device ids in devs and runs call. Music
// renumbers devices, so when a pid is missing or the call says "not found"
// it lists the devices again and retries once; the fresh list is returned.
func airplayCall(client bridge.Client, devs []bridge.AirPlayDevice, pids []string, call func(context.Context, []int) error) ([]bridge.AirPlayDevice, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*bridge.DefaultTimeout)
	defer cancel()
	var fresh []bridge.AirPlayDevice
	for attempt := 0; ; attempt++ {
		ids, ok := airplayIDs(devs, pids)
		if ok {
			err := call(ctx, ids)
			var be *bridge.Error
			if err == nil || attempt > 0 || !errors.As(err, &be) || be.Code != bridge.ErrNotFound {
				return fresh, err
			}
		} else if attempt > 0 {
			return fresh, &bridge.Error{Code: bridge.ErrNotFound, Message: "AirPlay device not found"}
		}
		list, err := client.AirPlayDevices(ctx)
		if err != nil {
			return fresh, err
		}
		devs, fresh = list, list
	}
}

func airplayIDs(devs []bridge.AirPlayDevice, pids []string) ([]int, bool) {
	ids := make([]int, 0, len(pids))
	for _, p := range pids {
		i := slices.IndexFunc(devs, func(d bridge.AirPlayDevice) bool { return d.PID == p })
		if i < 0 {
			return nil, false
		}
		ids = append(ids, devs[i].ID)
	}
	return ids, true
}

// airplayErrText is the one-line note for a failed call on the device name.
func airplayErrText(err error, name string) string {
	var be *bridge.Error
	switch {
	case errors.As(err, &be) && be.Code == bridge.ErrMusicNotRunning:
		return "Music isn't running"
	case errors.As(err, &be) && be.Code == bridge.ErrAutomationDenied:
		return "encore isn't allowed to control Music"
	case errors.As(err, &be) && be.Code == bridge.ErrAutomationConsent:
		return "Waiting for permission to control Music…"
	case name == "":
		return "Couldn't list AirPlay devices"
	}
	return name + " didn't respond"
}

func (o *AirPlayOverlay) move(d int) {
	o.cur.Step(d, len(o.devs), o.ok)
	o.cur.Follow(o.listH, len(o.devs), o.ok)
	o.syncFocus()
}

func (o *AirPlayOverlay) nudge(d int, c *Ctx) tea.Cmd {
	if !o.ok(o.cur.Sel) {
		return nil
	}
	return o.setVolume(o.cur.Sel, o.devs[o.cur.Sel].Volume+d, true, c)
}

func (o *AirPlayOverlay) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	switch a {
	case keymap.NavDown, keymap.InputNext:
		o.move(n)
	case keymap.NavUp, keymap.InputPrev:
		o.move(-n)
	case keymap.NavTop:
		o.cur.First(len(o.devs), o.ok)
		o.syncFocus()
	case keymap.NavBottom:
		o.move(len(o.devs))
	case keymap.FocusRight, keymap.PlayerVolUp:
		return true, o.nudge(airplayStep*n, c)
	case keymap.FocusLeft, keymap.PlayerVolDown:
		return true, o.nudge(-airplayStep*n, c)
	case keymap.Open, keymap.InputSubmit:
		return true, o.only(o.cur.Sel, c)
	case keymap.Back, keymap.InputCancel, keymap.AirPlay:
		return true, c.Host.CloseOverlay()
	default:
		return false, nil
	}
	return true, nil
}

// airplayKeys are the actions the picker honours from raw keys, under the
// user's bindings.
var airplayKeys = []keymap.Action{
	keymap.NavDown, keymap.NavUp, keymap.NavBottom,
	keymap.FocusLeft, keymap.FocusRight, keymap.PlayerVolDown, keymap.PlayerVolUp,
}

func (o *AirPlayOverlay) Key(msg tea.KeyPressMsg, c *Ctx) tea.Cmd {
	switch msg.String() {
	case "space", " ":
		return o.toggle(o.cur.Sel, c)
	case "home":
		_, cmd := o.Action(keymap.NavTop, 1, c)
		return cmd
	}
	if a := ovKeyAction(c, msg, airplayKeys...); a != "" {
		_, cmd := o.Action(a, 1, c)
		return cmd
	}
	return nil
}

// ovKeyAction resolves a raw key press to the first of acts it is bound to
// (single-key bindings only), for overlays that capture input but still
// follow the user's motions.
func ovKeyAction(c *Ctx, msg tea.KeyPressMsg, acts ...keymap.Action) keymap.Action {
	keys := c.Keys
	if keys == nil {
		keys = ovDefaultKeys()
	}
	d := keymap.Display([]string{keymap.Token(msg)})
	for _, a := range acts {
		if slices.Contains(keys.KeysFor(a), d) {
			return a
		}
	}
	return ""
}

var ovDefaultKeys = sync.OnceValue(keymap.New)

// meter is the local x range of the volume meter; the number follows it.
func (o *AirPlayOverlay) meter() (x0, x1 int) {
	x1 = o.w - 2 - 1 - 3 - 1 // border, padding, number, gap
	return x1 - airplayMeterW, x1
}

func (o *AirPlayOverlay) volumeAt(x int) int {
	x0, _ := o.meter()
	return min(max((x-x0+1)*100/airplayMeterW, 0), 100)
}

func (o *AirPlayOverlay) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	row := -1
	if y := ev.Y - o.listY; y >= 0 && y < o.listH && o.cur.Off+y < len(o.devs) && ev.X > 0 && ev.X < o.w-1 {
		row = o.cur.Off + y
	}
	x0, _ := o.meter()
	onMeter := row >= 0 && ev.X >= x0-1 && o.devs[row].Selected && o.devs[row].Available
	if o.drag != "" {
		i := slices.IndexFunc(o.devs, func(d bridge.AirPlayDevice) bool { return d.PID == o.drag })
		switch {
		case ev.Kind == MouseMotion && ev.Button == tea.MouseLeft:
			return o.setVolume(i, o.volumeAt(ev.X), false, c)
		case ev.Kind == MouseRelease:
			o.drag = ""
			if i < 0 {
				return nil
			}
			p := o.vol[o.devs[i].PID]
			p.until = time.Time{}
			o.vol[o.devs[i].PID] = p
			return o.setVolume(i, o.volumeAt(ev.X), true, c)
		}
	}
	switch ev.Kind {
	case MouseMotion:
		o.cur.Hover = -1
		if o.ok(row) {
			o.cur.Hover = row
		}
	case MouseWheel:
		if onMeter {
			d := airplayStep
			if ev.Button == tea.MouseWheelDown {
				d = -d
			}
			return o.setVolume(row, o.devs[row].Volume+d, true, c)
		}
		o.cur.Scroll(ovWheel(ev), o.listH, len(o.devs))
	case MousePress:
		if !o.ok(row) {
			return nil
		}
		o.cur.Sel = row
		o.syncFocus()
		switch {
		case ev.Button == tea.MouseRight:
			return o.only(row, c)
		case ev.Button != tea.MouseLeft:
			return nil
		case onMeter:
			o.drag = o.devs[row].PID
			return o.setVolume(row, o.volumeAt(ev.X), false, c)
		}
		return o.toggle(row, c)
	}
	return nil
}

func (o *AirPlayOverlay) Render(c *Ctx) string {
	t := c.Theme
	w, h := o.w, o.h
	if w == 0 {
		w, h = airplayW, 4+o.headH()+max(1, len(o.devs))
	}
	inner, ih := w-2, h-2
	if inner < 6 || ih < 1 {
		return ovFrame(t, w, h, nil)
	}
	hints := ih >= 4
	var body []string
	head := o.headH()
	if head > 0 {
		body = append(body, ovRow(t, o.heading(c, inner-3), inner-1, false, false)+" ")
	}
	o.listY = 1 + head
	o.listH = ih - head
	if hints {
		o.listH -= 2
	}
	o.listH = max(0, o.listH)
	o.cur.Clamp(o.listH, len(o.devs))
	switch {
	case len(o.devs) == 0 && o.listH > 0:
		msg := "No AirPlay devices found"
		if !o.loaded {
			msg = "Looking for devices…"
		}
		body = append(body, ovCenterLine(t, msg, inner))
	default:
		for y := 0; y < o.listH && o.cur.Off+y < len(o.devs); y++ {
			i := o.cur.Off + y
			bar := ovBar(t, y, o.listH, o.cur.Off, len(o.devs))
			sel := i == o.cur.Sel && o.ok(i)
			hov := i == o.cur.Hover
			body = append(body, ovRow(t, o.row(c, i, inner-3, sel || hov), inner-1, sel, hov)+bar)
		}
	}
	if hints {
		for len(body) < ih-1 {
			body = append(body, "")
		}
		if o.note != "" {
			body = append(body, ovPad(t.Dim.Render(o.note), inner))
		} else {
			body = append(body, ovHints(t, inner, o.hints()...))
		}
	}
	return ovFrame(t, w, h, body)
}

// heading is the dim "Speaker Volume" over the meters, cw cells wide: each
// meter is that speaker's own level, not Music's volume in the bar.
func (o *AirPlayOverlay) heading(c *Ctx, cw int) string {
	rightW := airplayMeterW + 4
	return strings.Repeat(" ", max(0, cw-rightW)) + FitRight(c.Theme.Dim.Render("Speaker Volume"), rightW)
}

// nameCol is the width of the name column when names and kinds share
// nameW cells: the longest name, so every kind starts in the same column.
// kinds is false when the longest name and kind don't both fit; then only
// names show.
func (o *AirPlayOverlay) nameCol(nameW int) (col int, kinds bool) {
	nw, kw := 0, 0
	for _, d := range o.devs {
		nw = max(nw, Width(d.Name))
		kw = max(kw, Width(airplayKind(d.Kind)))
	}
	if kw > 0 && nw+2+kw <= nameW {
		return nw, true
	}
	return nameW, false
}

// nameKind is a device's name and kind, the kind starting at the shared
// column, nameW cells wide.
func (o *AirPlayOverlay) nameKind(name, kind string, nameW int) string {
	col, kinds := o.nameCol(nameW)
	if !kinds || kind == "" {
		return Fit(name, nameW)
	}
	return Fit(Fit(name, col)+"  "+kind, nameW)
}

// row is device i's line, cw cells wide: mark, name, kind, then the meter
// and volume for a playing device or "unavailable".
func (o *AirPlayOverlay) row(c *Ctx, i, cw int, lit bool) string {
	t := c.Theme
	d := o.devs[i]
	rightW := airplayMeterW + 4
	nameW := max(0, cw-2-2-rightW)
	k := airplayKind(d.Kind)
	if !d.Available {
		name := o.nameKind(t.Dim.Render(d.Name), t.Faint.Render(k), nameW)
		return "  " + name + "  " + FitRight(t.Dim.Render("unavailable"), rightW)
	}
	mark := t.Faint.Render("○")
	switch {
	case d.Selected:
		mark = t.Accent.Render("●")
	case lit:
		mark = t.Dim.Render("○")
	}
	name := o.nameKind(t.Text.Render(d.Name), t.Dim.Render(k), nameW)
	right := ""
	if d.Selected {
		right = o.meterText(c, d.Volume, lit || o.drag == d.PID) + " " + FitRight(t.Dim.Render(itoa(d.Volume)), 3)
	}
	return mark + " " + name + "  " + Fit(right, rightW)
}

func (o *AirPlayOverlay) meterText(c *Ctx, v int, lit bool) string {
	t := c.Theme
	g := t.G
	n := int(math.Round(float64(v) * airplayMeterW / 100))
	fill := t.Dim
	if lit {
		fill = t.Text
	}
	return fill.Render(strings.Repeat(g.BarFill, n)) + t.Faint.Render(strings.Repeat(g.BarRest, airplayMeterW-n))
}

// airplayKind names a device kind the way Music's AirPlay menu does.
func airplayKind(k string) string {
	switch k {
	case "computer":
		return "Computer"
	case "homepod":
		return "HomePod"
	case "speaker":
		return "Speaker"
	case "tv":
		return "Apple TV"
	}
	return ""
}

func (o *AirPlayOverlay) hints() []ovHint {
	if !o.ok(o.cur.Sel) {
		return []ovHint{{"esc", "close"}}
	}
	d := o.devs[o.cur.Sel]
	switch {
	case !d.Selected:
		return []ovHint{{"click", "to add"}, {"enter", "only this"}, {"esc", "close"}}
	case len(o.selectedPIDs()) == 1:
		return []ovHint{{"click", "others to add"}, {"←→", "volume"}, {"esc", "close"}}
	}
	return []ovHint{{"click", "to remove"}, {"enter", "only this"}, {"←→", "volume"}}
}
