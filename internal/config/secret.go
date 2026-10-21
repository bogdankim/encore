package config

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SecretService is the Keychain service name for every encore secret.
const SecretService = "encore"

// ErrNoSecret means no secret is stored under the account.
var ErrNoSecret = errors.New("config: secret not found")

// securityPath is a variable so tests can force the file fallback.
var securityPath = "/usr/bin/security"

// errSecItemNotFound is security(1)'s exit status for a missing item.
const errSecItemNotFound = 44

// securityTimeout bounds each security(1) run. It can block on a Keychain
// dialog; past this the file fallback is used instead.
const securityTimeout = 20 * time.Second

func security(args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), securityTimeout)
	cmd := exec.CommandContext(ctx, securityPath, args...)
	cmd.WaitDelay = time.Second
	return cmd, cancel
}

var (
	accountRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	fileMu    sync.Mutex
)

// SecretGet returns the secret stored for account, or ErrNoSecret.
// It reads the Keychain first and the fallback file second.
func SecretGet(account string) (string, error) {
	if err := checkAccount(account); err != nil {
		return "", err
	}
	v, err := keychainGet(account)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, ErrNoSecret) && !errors.Is(err, errKeychainUnavailable) {
		return "", err
	}
	return fileGet(account)
}

// SecretSet stores value for account in the Keychain, or in a 0600 file in
// StateDir when the Keychain can't be used.
func SecretSet(account, value string) error {
	if err := checkAccount(account); err != nil {
		return err
	}
	if err := keychainSet(account, value); err == nil {
		// Don't leave an older copy behind in the fallback file.
		if err := fileDelete(account); err != nil && !errors.Is(err, ErrNoSecret) {
			return err
		}
		return nil
	}
	return fileSet(account, value)
}

// SecretDelete removes the secret from both stores. Deleting a missing
// secret is not an error.
func SecretDelete(account string) error {
	if err := checkAccount(account); err != nil {
		return err
	}
	kerr := keychainDelete(account)
	ferr := fileDelete(account)
	if kerr != nil && !errors.Is(kerr, ErrNoSecret) && !errors.Is(kerr, errKeychainUnavailable) {
		return kerr
	}
	if ferr != nil && !errors.Is(ferr, ErrNoSecret) {
		return ferr
	}
	return nil
}

func checkAccount(a string) error {
	if !accountRe.MatchString(a) {
		return fmt.Errorf("config: invalid secret account %q", a)
	}
	return nil
}

// --- Keychain via security(1) ----------------------------------------------

var errKeychainUnavailable = errors.New("config: keychain unavailable")

func keychainGet(account string) (string, error) {
	// -g prints `password: "text"` or, for anything not plain printable
	// ASCII, `password: 0xHEX  "escaped"` on stderr. -w alone is ambiguous
	// because it switches to bare hex for the same cases.
	cmd, cancel := security("find-generic-password", "-s", SecretService, "-a", account, "-g")
	defer cancel()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == errSecItemNotFound {
			return "", ErrNoSecret
		}
		return "", fmt.Errorf("%w: %v", errKeychainUnavailable, err)
	}
	sc := bufio.NewScanner(&stderr)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "password: ")
		if !ok {
			continue
		}
		if h, ok := strings.CutPrefix(line, "0x"); ok {
			if i := strings.IndexByte(h, ' '); i >= 0 {
				h = h[:i]
			}
			b, err := hex.DecodeString(h)
			if err != nil {
				return "", fmt.Errorf("config: keychain: bad hex password: %v", err)
			}
			return string(b), nil
		}
		if len(line) >= 2 && line[0] == '"' && line[len(line)-1] == '"' {
			return line[1 : len(line)-1], nil
		}
		if line == "" {
			return "", nil
		}
		return "", fmt.Errorf("config: keychain: unexpected output %q", line)
	}
	return "", nil // item exists with no password data
}

func keychainSet(account, value string) error {
	// Commands go through stdin (`security -i`) so the secret never shows up
	// in the process list; hex (-X) avoids any quoting.
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -l %s -X %s\n",
		SecretService, account, SecretService, hex.EncodeToString([]byte(value)))
	if value == "" {
		line = fmt.Sprintf("add-generic-password -U -s %s -a %s -l %s -w \"\"\n", SecretService, account, SecretService)
	}
	cmd, cancel := security("-i")
	defer cancel()
	cmd.Stdin = strings.NewReader(line)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %v: %s", errKeychainUnavailable, err, strings.TrimSpace(out.String()))
	}
	// security -i exits 0 even when a command fails; it reports "returned N".
	if strings.Contains(out.String(), "returned") {
		return fmt.Errorf("%w: %s", errKeychainUnavailable, strings.TrimSpace(out.String()))
	}
	return nil
}

func keychainDelete(account string) error {
	cmd, cancel := security("delete-generic-password", "-s", SecretService, "-a", account)
	defer cancel()
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == errSecItemNotFound {
			return ErrNoSecret
		}
		return fmt.Errorf("%w: %v", errKeychainUnavailable, err)
	}
	return nil
}

// --- 0600 file fallback ------------------------------------------------------

func secretsPath() string { return filepath.Join(StateDir(), "secrets.json") }

func readSecrets() (map[string]string, error) {
	m := map[string]string{}
	data, err := os.ReadFile(secretsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("config: %s: %w", secretsPath(), err)
	}
	return m, nil
}

func writeSecrets(m map[string]string) error {
	if len(m) == 0 {
		err := os.Remove(secretsPath())
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := EnsureDir(StateDir()); err != nil {
		return err
	}
	return writeFileAtomic(secretsPath(), data, 0o600)
}

func fileGet(account string) (string, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	m, err := readSecrets()
	if err != nil {
		return "", err
	}
	v, ok := m[account]
	if !ok {
		return "", ErrNoSecret
	}
	return v, nil
}

func fileSet(account, value string) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	m, err := readSecrets()
	if err != nil {
		return err
	}
	m[account] = value
	return writeSecrets(m)
}

func fileDelete(account string) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	m, err := readSecrets()
	if err != nil {
		return err
	}
	if _, ok := m[account]; !ok {
		return ErrNoSecret
	}
	delete(m, account)
	return writeSecrets(m)
}

// writeFileAtomic writes via a temp file in the same directory and renames it
// into place, so readers never see a partial file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// WriteFileAtomic is writeFileAtomic for other packages' state files.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}
	return writeFileAtomic(path, data, perm)
}
