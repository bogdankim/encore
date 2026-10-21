package ui

import (
	"context"
	"errors"
	"image"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/lastfm"
	"github.com/bogdankim/encore/internal/theme"
)

const (
	lastfmW = 64
	// The setup and the connected screen have fixed heights, so the frame
	// doesn't jump while typing or when an error comes and goes.
	lastfmSetupH     = 19
	lastfmConnectedH = 15
	// lastfmIndent puts step content under the step title.
	lastfmIndent = 3
	lastfmLabelW = 8
)

// LastfmEnv is everything the last.fm setup touches outside the UI. Zero
// fields use the real thing: the Keychain, `open`, the process environment
// and the live API.
type LastfmEnv struct {
	Auth   *lastfm.Auth // template for HTTP, Root, Interval and Timeout
	Load   func() (lastfm.Credentials, error)
	Save   func(lastfm.Credentials) error
	Delete func() error
	Open   func(url string) error
	Getenv func(string) string
}

func (e LastfmEnv) withDefaults() LastfmEnv {
	if e.Auth == nil {
		e.Auth = &lastfm.Auth{}
	}
	if e.Load == nil {
		e.Load = lastfm.LoadCredentials
	}
	if e.Save == nil {
		e.Save = lastfm.SaveCredentials
	}
	if e.Delete == nil {
		e.Delete = lastfm.DeleteCredentials
	}
	if e.Open == nil {
		e.Open = func(url string) error { return exec.Command("open", url).Run() }
	}
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	return e
}

type lfMode uint8

const (
	lfSetup     lfMode = iota
	lfChecking         // BeginAuth is validating the key
	lfWaiting          // the user is approving in the browser
	lfConnected        // credentials are ready
)

// lfElem is a focusable line.
type lfElem uint8

const (
	lfNone lfElem = iota
	lfOpen
	lfKey
	lfSecret
	lfConnect
	lfReopen
	lfToggle
	lfReauth
	lfDisconnect
)

func (e lfElem) field() bool { return e == lfKey || e == lfSecret }

type (
	lfLoadedMsg struct {
		owner *LastfmOverlay
		creds lastfm.Credentials
	}
	lfOpenedMsg struct {
		owner *LastfmOverlay
		step  int
		err   error
	}
	lfBeginMsg struct {
		owner      *LastfmOverlay
		gen        int
		auth       *lastfm.Auth
		url, token string
		err        error
	}
	lfSessionMsg struct {
		owner   *LastfmOverlay
		gen     int
		creds   lastfm.Credentials
		err     error
		saveErr error
	}
	lfTickMsg struct {
		owner *LastfmOverlay
		gen   int
	}
	lfDisconnectedMsg struct {
		owner *LastfmOverlay
		err   error
	}
)

// LastfmOverlay connects encore to last.fm in three steps on one screen,
// and once connected shows the scrobbling state with on/off, re-authorize
// and disconnect.
type LastfmOverlay struct {
	sc  *lastfm.Scrobbler
	env LastfmEnv

	mode        lfMode
	focus       lfElem
	hover       lfElem
	key, secret ovInput
	envKeys     bool // key and secret come from the environment
	opened      bool // the API account page was opened
	err         string
	errStep     int // the step err belongs to: 2 or 3
	saveErr     string
	user        string
	off         bool // scrobbling switched off in this session
	armed       bool // Disconnect asks once more
	started     bool

	gen     int
	authURL string
	since   time.Time
	cancel  context.CancelFunc

	hits []lfElem // focusable element on each local row, from the last render
	w, h int
}

// NewLastfmSetup opens the last.fm setup for sc.
func NewLastfmSetup(sc *lastfm.Scrobbler) Overlay { return NewLastfmSetupEnv(sc, LastfmEnv{}) }

// NewLastfmSetupEnv is NewLastfmSetup with the outside world replaced, for
// tests and previews.
func NewLastfmSetupEnv(sc *lastfm.Scrobbler, env LastfmEnv) Overlay {
	o := &LastfmOverlay{sc: sc, env: env.withDefaults()}
	o.key = newOvInput("Paste the API key")
	o.secret = newOvInput("Paste the shared secret")
	o.secret.m.EchoMode = textinput.EchoPassword
	o.secret.m.EchoCharacter = '•'
	k := strings.TrimSpace(o.env.Getenv(lastfm.EnvKey))
	s := strings.TrimSpace(o.env.Getenv(lastfm.EnvSecret))
	o.key.SetValue(k)
	o.secret.SetValue(s)
	o.envKeys = k != "" && s != ""
	st := o.status()
	switch {
	case st.User != "":
		o.mode, o.user, o.off = lfConnected, st.User, !st.Enabled
		o.setFocus(lfToggle)
	case o.envKeys:
		o.setFocus(lfConnect)
	default:
		o.setFocus(lfOpen)
	}
	return o
}

func (o *LastfmOverlay) status() lastfm.Status {
	if o.sc == nil {
		return lastfm.Status{}
	}
	return o.sc.Status()
}

// Init reads the stored key and secret, so re-authorizing doesn't ask for
// them again.
func (o *LastfmOverlay) Init(c *Ctx) tea.Cmd {
	if o.started {
		return nil
	}
	o.started = true
	load := o.env.Load
	return func() tea.Msg {
		creds, _ := load()
		return lfLoadedMsg{owner: o, creds: creds}
	}
}

// Close stops waiting for approval. Call it when the overlay goes away by
// any route other than its own esc.
func (o *LastfmOverlay) Close() {
	if o.cancel != nil {
		o.cancel()
		o.cancel = nil
	}
	o.gen++
}

func (o *LastfmOverlay) Rect(screenW, screenH int) image.Rectangle {
	h := lastfmSetupH
	if o.mode == lfConnected {
		h = lastfmConnectedH
	}
	r := ovPlace(screenW, screenH, lastfmW, h)
	o.w, o.h = r.Dx(), r.Dy()
	return r
}

func (o *LastfmOverlay) CapturesInput() bool { return true }

// elems are the focusable lines in order.
func (o *LastfmOverlay) elems() []lfElem {
	switch o.mode {
	case lfConnected:
		return []lfElem{lfToggle, lfReauth, lfDisconnect}
	case lfWaiting:
		return []lfElem{lfReopen}
	case lfChecking:
		return nil
	}
	if o.envKeys {
		return []lfElem{lfConnect}
	}
	return []lfElem{lfOpen, lfKey, lfSecret, lfConnect}
}

func (o *LastfmOverlay) setFocus(e lfElem) {
	if e != lfDisconnect {
		o.armed = false
	}
	o.focus = e
	o.key.Blur()
	o.secret.Blur()
	switch e {
	case lfKey:
		o.key.Focus()
	case lfSecret:
		o.secret.Focus()
	}
}

func (o *LastfmOverlay) move(d int) {
	es := o.elems()
	if len(es) == 0 {
		return
	}
	i := slices.Index(es, o.focus)
	if i < 0 {
		o.setFocus(es[0])
		return
	}
	o.setFocus(es[(i+d%len(es)+len(es))%len(es)])
}

func (o *LastfmOverlay) values() (key, secret string) {
	return strings.TrimSpace(o.key.Value()), strings.TrimSpace(o.secret.Value())
}

// stepDone reports whether step n of the setup is complete.
func (o *LastfmOverlay) stepDone(n int) bool {
	k, s := o.values()
	switch n {
	case 1:
		return o.opened || o.envKeys || k != ""
	case 2:
		return k != "" && s != ""
	}
	return false
}

func (o *LastfmOverlay) stepOf(e lfElem) int {
	switch e {
	case lfOpen:
		return 1
	case lfKey, lfSecret:
		return 2
	}
	return 3
}

// open opens url in the browser; a failure shows under step.
func (o *LastfmOverlay) open(url string, step int) tea.Cmd {
	open := o.env.Open
	return func() tea.Msg { return lfOpenedMsg{owner: o, step: step, err: open(url)} }
}

func (o *LastfmOverlay) activate(e lfElem, c *Ctx) tea.Cmd {
	switch e {
	case lfOpen:
		o.opened = true
		o.setFocus(lfKey)
		return o.open(lastfm.AccountCreateURL, 1)
	case lfKey:
		o.setFocus(lfSecret)
	case lfSecret, lfConnect:
		return o.connect()
	case lfReopen:
		return o.open(o.authURL, 3)
	case lfToggle:
		o.off = !o.off
		if o.sc != nil {
			o.sc.SetEnabled(!o.off)
		}
	case lfReauth:
		o.mode, o.err, o.saveErr = lfSetup, "", ""
		switch {
		case o.envKeys || o.stepDone(2):
			o.setFocus(lfConnect)
		default:
			o.setFocus(lfOpen)
		}
	case lfDisconnect:
		if !o.armed {
			o.armed = true
			return nil
		}
		o.armed = false
		del, sc := o.env.Delete, o.sc
		return func() tea.Msg {
			err := del()
			if sc != nil {
				sc.SetCredentials(lastfm.Credentials{})
			}
			return lfDisconnectedMsg{owner: o, err: err}
		}
	}
	return nil
}

// connect validates the key and secret with auth.getToken.
func (o *LastfmOverlay) connect() tea.Cmd {
	key, secret := o.values()
	if key == "" || secret == "" {
		o.err, o.errStep = "Paste both the API key and the shared secret.", 2
		if key == "" {
			o.setFocus(lfKey)
		} else {
			o.setFocus(lfSecret)
		}
		return nil
	}
	o.err, o.saveErr = "", ""
	o.mode = lfChecking
	o.setFocus(lfConnect)
	o.gen++
	gen, tmpl := o.gen, o.env.Auth
	return func() tea.Msg {
		a := &lastfm.Auth{HTTP: tmpl.HTTP, Root: tmpl.Root, Interval: tmpl.Interval, Timeout: tmpl.Timeout}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		url, token, err := a.BeginAuth(ctx, key, secret)
		return lfBeginMsg{owner: o, gen: gen, auth: a, url: url, token: token, err: err}
	}
}

// wait polls for the session. The credentials are saved and handed to the
// scrobbler here, so an approval still lands if the overlay is closed
// without cancelling.
func (o *LastfmOverlay) wait(ctx context.Context, a *lastfm.Auth, token string) tea.Cmd {
	gen, save, sc := o.gen, o.env.Save, o.sc
	return func() tea.Msg {
		creds, err := a.WaitSession(ctx, token)
		msg := lfSessionMsg{owner: o, gen: gen, creds: creds, err: err}
		if err == nil {
			msg.saveErr = save(creds)
			if sc != nil {
				sc.SetCredentials(creds)
			}
		}
		return msg
	}
}

func (o *LastfmOverlay) tick() tea.Cmd {
	gen := o.gen
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return lfTickMsg{owner: o, gen: gen} })
}

func (o *LastfmOverlay) Update(msg tea.Msg, c *Ctx) tea.Cmd {
	var init tea.Cmd
	if !o.started {
		init = o.Init(c)
	}
	switch m := msg.(type) {
	case lfLoadedMsg:
		if m.owner == o && !o.envKeys {
			o.prefill(m.creds)
		}
	case lfOpenedMsg:
		if m.owner == o && m.err != nil {
			o.err, o.errStep = "Couldn't open the browser.", m.step
		}
	case lfBeginMsg:
		if m.owner != o || m.gen != o.gen || o.mode != lfChecking {
			break
		}
		if m.err != nil {
			o.mode = lfSetup
			o.err, o.errStep = lastfmErrText(m.err)
			if o.errStep == 2 && !o.envKeys {
				o.setFocus(lfKey)
			}
			break
		}
		ctx, cancel := context.WithCancel(context.Background())
		o.cancel = cancel
		o.mode, o.authURL, o.since = lfWaiting, m.url, c.Now
		o.setFocus(lfReopen)
		return tea.Batch(init, o.open(m.url, 3), o.wait(ctx, m.auth, m.token), o.tick())
	case lfSessionMsg:
		if m.owner != o || m.gen != o.gen || o.mode != lfWaiting {
			break
		}
		o.Close()
		if m.err != nil {
			o.mode = lfSetup
			o.err, o.errStep = lastfmErrText(m.err)
			o.setFocus(lfConnect)
			break
		}
		o.mode, o.user, o.off = lfConnected, m.creds.Username, false
		if m.saveErr != nil {
			o.saveErr = "Couldn't save to the Keychain; this lasts until you quit."
		}
		o.setFocus(lfToggle)
	case lfTickMsg:
		if m.owner == o && m.gen == o.gen && o.mode == lfWaiting {
			return tea.Batch(init, o.tick())
		}
	case lfDisconnectedMsg:
		if m.owner != o {
			break
		}
		o.mode, o.user, o.off = lfSetup, "", false
		o.setFocus(lfConnect)
		if m.err != nil {
			o.err, o.errStep = "Disconnected, but the Keychain kept the old credentials.", 3
		}
	case tea.PasteMsg:
		if c.Focused {
			o.paste(m.Content)
		}
	}
	return init
}

func (o *LastfmOverlay) prefill(creds lastfm.Credentials) {
	if o.key.Value() == "" && o.secret.Value() == "" && creds.APIKey != "" && creds.Secret != "" {
		o.key.SetValue(creds.APIKey)
		o.secret.SetValue(creds.Secret)
		if o.mode == lfSetup && o.focus == lfOpen {
			o.setFocus(lfConnect)
		}
	}
}

var lastfmHex32 = regexp.MustCompile(`(?i)\b[0-9a-f]{32}\b`)

// paste puts pasted text in the focused field. Text holding two 32-digit
// hex strings (the whole block copied from last.fm's API page) fills both
// fields at once.
func (o *LastfmOverlay) paste(s string) {
	if o.mode != lfSetup || o.envKeys {
		return
	}
	if ms := lastfmHex32.FindAllString(s, -1); len(ms) == 2 {
		o.key.SetValue(ms[0])
		o.secret.SetValue(ms[1])
		o.setFocus(lfConnect)
		o.clearFieldErr()
		return
	}
	if o.field() == nil {
		if k, _ := o.values(); k == "" {
			o.setFocus(lfKey)
		} else {
			o.setFocus(lfSecret)
		}
	}
	in := o.field()
	if ms := lastfmHex32.FindAllString(s, -1); len(ms) == 1 {
		s = ms[0]
	}
	in.m, _ = in.m.Update(tea.PasteMsg{Content: strings.TrimSpace(singleLine(s))})
	o.clearFieldErr()
}

func (o *LastfmOverlay) field() *ovInput {
	switch o.focus {
	case lfKey:
		return &o.key
	case lfSecret:
		return &o.secret
	}
	return nil
}

func (o *LastfmOverlay) clearFieldErr() {
	if o.errStep == 2 {
		o.err = ""
	}
}

// lastfmErrText explains a setup failure and names the step it belongs to.
func lastfmErrText(err error) (string, int) {
	var ae *lastfm.APIError
	switch {
	case errors.Is(err, lastfm.ErrAuthTimeout):
		return "The approval timed out. Connect to try again.", 3
	case errors.As(err, &ae):
		switch ae.Code {
		case 10:
			return "last.fm doesn't recognise this API key.", 2
		case 13:
			return "This secret doesn't belong to the API key.", 2
		case 26:
			return "last.fm has suspended this API key.", 2
		case 4, 15:
			return "The approval expired. Connect to try again.", 3
		case 11, 16, 29:
			return "last.fm is busy right now. Try again in a minute.", 3
		case -1:
			return "last.fm sent an unexpected reply. Try again.", 3
		}
		return "last.fm: " + ae.Message, 3
	}
	return "Couldn't reach last.fm. Check the connection.", 3
}

func (o *LastfmOverlay) close(c *Ctx) tea.Cmd {
	o.Close()
	return c.Host.CloseOverlay()
}

func (o *LastfmOverlay) Action(a keymap.Action, n int, c *Ctx) (bool, tea.Cmd) {
	switch a {
	case keymap.InputNextScope, keymap.InputNext, keymap.NavDown:
		o.move(n)
	case keymap.InputPrevScope, keymap.InputPrev, keymap.NavUp:
		o.move(-n)
	case keymap.InputSubmit, keymap.Open:
		return true, o.activate(o.focus, c)
	case keymap.InputCancel, keymap.Back, keymap.SettingsLastfm:
		return true, o.close(c)
	default:
		return false, nil
	}
	return true, nil
}

func (o *LastfmOverlay) Key(msg tea.KeyPressMsg, c *Ctx) tea.Cmd {
	if in := o.field(); in != nil && o.mode == lfSetup {
		if in.Key(msg) {
			o.clearFieldErr()
		}
		return nil
	}
	if msg.String() == "space" {
		return o.activate(o.focus, c)
	}
	switch ovKeyAction(c, msg, keymap.NavDown, keymap.NavUp) {
	case keymap.NavDown:
		o.move(1)
	case keymap.NavUp:
		o.move(-1)
	}
	return nil
}

func (o *LastfmOverlay) Mouse(ev MouseEvent, c *Ctx) tea.Cmd {
	e := lfNone
	if ev.Y >= 0 && ev.Y < len(o.hits) && ev.X > 0 && ev.X < o.w-1 {
		e = o.hits[ev.Y]
	}
	switch ev.Kind {
	case MouseMotion:
		o.hover = e
	case MousePress:
		if ev.Button != tea.MouseLeft || e == lfNone {
			return nil
		}
		o.setFocus(e)
		if !e.field() {
			return o.activate(e, c)
		}
	}
	return nil
}

// lfBody collects body lines and the element on each.
type lfBody struct {
	lines []string
	hits  []lfElem
}

func (b *lfBody) add(line string, e lfElem) {
	b.lines = append(b.lines, line)
	b.hits = append(b.hits, e)
}

func (o *LastfmOverlay) Render(c *Ctx) string {
	t := c.Theme
	w, h := o.w, o.h
	if w == 0 {
		w, h = lastfmW, lastfmSetupH
	}
	inner, ih := w-2, h-2
	if inner < 12 || ih < 1 {
		o.hits = nil
		return ovFrame(t, w, h, nil)
	}
	b := &lfBody{}
	if o.mode == lfConnected {
		o.renderConnected(c, b, inner)
	} else {
		o.renderSetup(c, b, inner)
	}
	if ih >= 4 {
		for len(b.lines) < ih-1 {
			b.add("", lfNone)
		}
		b.lines = append(b.lines[:ih-1], ovHints(t, inner, o.hints()...))
		b.hits = b.hits[:ih-1]
	}
	o.hits = append([]lfElem{lfNone}, b.hits...)
	return ovFrame(t, w, h, b.lines)
}

// item is a focusable line with the selection marker and background of the
// overlay lists. Fields keep only the marker: their own well and cursor
// show the focus.
func (o *LastfmOverlay) item(c *Ctx, b *lfBody, e lfElem, content string, inner int) {
	if e.field() {
		mark := " "
		if o.focus == e {
			mark = c.Theme.Accent.Render(c.Theme.G.Marker)
		}
		b.add(mark+Fit(content, inner-2)+" ", e)
		return
	}
	b.add(ovRow(c.Theme, content, inner, o.focus == e, o.hover == e && o.focus != e), e)
}

func (o *LastfmOverlay) renderSetup(c *Ctx, b *lfBody, inner int) {
	t := c.Theme
	cw := inner - 2
	ind := strings.Repeat(" ", lastfmIndent)
	note := func(s string) { b.add(ovPad(ind+t.Dim.Render(s), inner), lfNone) }
	errLine := func(step int) {
		if o.err != "" && o.errStep == step {
			b.add(ovPad(ind+t.Warn.Render(o.err), inner), lfNone)
		}
		b.add("", lfNone)
	}
	cur := o.stepOf(o.focus)

	b.add(ovPad(t.Bold.Render("Connect last.fm"), inner), lfNone)
	b.add(ovPad(t.Dim.Render("Scrobble what you play in encore to your last.fm profile."), inner), lfNone)
	b.add("", lfNone)

	b.add(o.stepTitle(c, 1, "Create an API account", cur, inner), lfNone)
	if o.envKeys {
		note("Not needed: the key comes from the environment.")
		b.add("", lfNone)
	} else {
		o.item(c, b, lfOpen, ind+t.Text.Render("Open last.fm’s API page"), inner)
		note("Any name works. Leave the callback URL empty.")
	}
	errLine(1)

	b.add(o.stepTitle(c, 2, "Paste your API key and secret", cur, inner), lfNone)
	if o.envKeys {
		note(Fit("Key", lastfmLabelW) + "from " + lastfm.EnvKey)
		note(Fit("Secret", lastfmLabelW) + "from " + lastfm.EnvSecret)
	} else {
		o.item(c, b, lfKey, o.fieldLine(c, &o.key, "Key", cw), inner)
		o.item(c, b, lfSecret, o.fieldLine(c, &o.secret, "Secret", cw), inner)
	}
	errLine(2)

	b.add(o.stepTitle(c, 3, "Approve encore in your browser", cur, inner), lfNone)
	switch o.mode {
	case lfChecking:
		note("Checking the key…")
		b.add("", lfNone)
	case lfWaiting:
		el := c.Now.Sub(o.since).Round(time.Second)
		b.add(ovPad(ind+t.Text.Render("Waiting for approval")+t.Dim.Render("  "+clock(max(0, el))), inner), lfNone)
		o.item(c, b, lfReopen, ind+t.Text.Render("Open the approval page again"), inner)
	default:
		o.item(c, b, lfConnect, ind+t.Text.Render("Connect"), inner)
		if o.err != "" && o.errStep == 3 {
			b.add(ovPad(ind+t.Warn.Render(o.err), inner), lfNone)
		} else {
			note("Your browser opens last.fm. Allow access, then come back.")
		}
	}
}

// stepTitle is "1  Title": a dim numeral, or a ✓ once done; the
// title is fg on the current step and dim elsewhere.
func (o *LastfmOverlay) stepTitle(c *Ctx, n int, title string, cur, inner int) string {
	t := c.Theme
	num := t.Dim.Render(itoa(n))
	if o.stepDone(n) {
		num = t.Text.Render("✓")
	}
	st := t.Dim
	if n == cur {
		st = t.Text
	}
	return ovPad(num+strings.Repeat(" ", lastfmIndent-1)+st.Render(title), inner)
}

// fieldLine is a labelled text field on a recessed background.
func (o *LastfmOverlay) fieldLine(c *Ctx, in *ovInput, label string, cw int) string {
	t := c.Theme
	in.style(t)
	fw := max(4, cw-lastfmIndent-lastfmLabelW)
	in.m.SetWidth(fw - 3)
	well := ovFill(" "+Fit(in.m.View(), fw-1), ovSGR(t.Selected))
	return strings.Repeat(" ", lastfmIndent) + Fit(t.Dim.Render(label), lastfmLabelW) + well
}

func (o *LastfmOverlay) renderConnected(c *Ctx, b *lfBody, inner int) {
	t := c.Theme
	cw := inner - 2
	st := o.status()
	user := o.user
	if st.User != "" {
		user = st.User
	}
	b.add(ovPad(t.Bold.Render("last.fm"), inner), lfNone)
	b.add("", lfNone)
	b.add(ovPad(t.Text.Render("Scrobbling as ")+t.Bold.Render(user), inner), lfNone)
	b.add(ovPad(lastfmStatusLine(t, st), inner), lfNone)
	b.add("", lfNone)

	state := t.Accent.Render("On")
	if o.off {
		state = t.Dim.Render("Off")
	}
	o.item(c, b, lfToggle, Row(2, Cell{Text: t.Text.Render("Scrobbling"), W: cw - 5}, Cell{Text: state, W: 3, Right: true}), inner)
	o.item(c, b, lfReauth, t.Text.Render("Re-authorize"), inner)
	disc := t.Text.Render("Disconnect")
	if o.armed {
		disc = t.Warn.Render("Disconnect") + t.Dim.Render("  press enter again to confirm")
	}
	o.item(c, b, lfDisconnect, disc, inner)
	b.add("", lfNone)
	if o.off {
		b.add(ovPad(t.Dim.Render("Off until encore quits. To keep it off, set"), inner), lfNone)
		b.add(ovPad(t.Dim.Render("enabled = false under [lastfm] in config.toml."), inner), lfNone)
	}
	if o.saveErr != "" {
		b.add(ovPad(t.Warn.Render(o.saveErr), inner), lfNone)
	}
}

// lastfmStatusLine is the queue state: waiting plays, up to date, or the
// problem in warn.
func lastfmStatusLine(t *theme.Theme, st lastfm.Status) string {
	waiting := ""
	if st.Queued > 0 {
		waiting = plural(st.Queued, "scrobble waiting", "scrobbles waiting")
	}
	switch st.State {
	case lastfm.StateReauth:
		return t.Warn.Render("last.fm signed encore out. Re-authorize to keep scrobbling.")
	case lastfm.StateConfig:
		return t.Warn.Render("last.fm rejected the API key. Re-authorize with a new one.")
	case lastfm.StateOffline:
		return t.Warn.Render("Can't reach last.fm") + t.Dim.Render(" · "+joinMeta("retrying", waiting))
	}
	switch {
	case st.LastError != "" && waiting != "":
		return t.Dim.Render(waiting+" · ") + t.Warn.Render(st.LastError)
	case st.LastError != "":
		return t.Warn.Render(st.LastError)
	case waiting != "":
		return t.Dim.Render(waiting)
	}
	return t.Dim.Render("Up to date")
}

func (o *LastfmOverlay) hints() []ovHint {
	switch o.mode {
	case lfChecking:
		return []ovHint{{"esc", "close"}}
	case lfWaiting:
		return []ovHint{{"enter", "open again"}, {"esc", "cancel"}}
	case lfConnected:
		verb := map[lfElem]string{lfToggle: "switch", lfReauth: "re-authorize", lfDisconnect: "disconnect"}[o.focus]
		return []ovHint{{"↑↓", "select"}, {"enter", verb}, {"esc", "close"}}
	}
	verb := map[lfElem]string{lfOpen: "open", lfKey: "next", lfSecret: "connect", lfConnect: "connect"}[o.focus]
	return []ovHint{{"tab", "next"}, {"enter", verb}, {"esc", "close"}}
}
