package pirpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// piBOM is the UTF-8 byte order mark, and stripPiBOM drops it.
//
// pi reads BOTH of its JSON config files as JSON.parse(stripBom(content))
// (verified in the installed pi 0.87.1 bundle,
// dist/bundle/chunks/chunk-OJP47DM6.js — the same stripBom call serves
// settings.json and auth.json), so a BOM-prefixed file is a perfectly valid
// pi configuration: PowerShell's `>` and several editors write UTF-8 with a
// BOM by default, and pi reads it happily.
//
// Go's encoding/json rejects the same bytes ("invalid character 'ï'"), so
// pitago has to strip the mark exactly like pi. Without this, a settings.json
// pi considers perfectly readable looks like an unreadable file to us: the
// /settings rows would silently show pi's defaults, and a write would be
// REFUSED with ErrUnreadableSettings — refusing a file pi itself edits.
const piBOM = "\xef\xbb\xbf"

// stripPiBOM drops a leading UTF-8 BOM, like pi's own stripBom.
func stripPiBOM(raw []byte) []byte { return bytes.TrimPrefix(raw, []byte(piBOM)) }

// hasPiBOM reports whether the file at path currently starts with a UTF-8
// BOM. It is used to put the mark back on a rewrite, so editing a
// BOM-prefixed settings.json/auth.json does not quietly reshape the file
// (pitago must change the value it was asked to change, nothing else).
func hasPiBOM(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var head [len(piBOM)]byte
	n, _ := f.Read(head[:])
	return n == len(piBOM) && string(head[:]) == piBOM
}

// settingsWriteAttempts bounds the compare-and-swap retries in
// SetPiSettingAt. pi saves settings.json itself (every set_steering_mode /
// set_auto_compaction / set_auto_retry queues one), so the file can change
// under us; three attempts is far beyond a real collision.
const settingsWriteAttempts = 3

// Refusal errors. Both mean the same thing to the user: pitago did not
// write, and nothing of theirs was lost.
var (
	// ErrUnreadableSettings is returned when settings.json exists but does
	// not parse as a JSON object. Rewriting it would DELETE the whole file
	// (pi keeps defaultProvider/defaultModel/defaultThinkingLevel/
	// packages/theme in it), so the write is refused and the file is left
	// byte-for-byte alone. The fix is the user's: fix or remove the file.
	ErrUnreadableSettings = errors.New("pi settings: refusing to rewrite an unreadable settings.json")

	// ErrSettingConflict is returned when an intermediate key of the
	// dotted path already holds a non-object value (e.g. "theme": "dark"
	// while writing "theme.name"). Replacing it would silently change the
	// user's pi configuration, so the write is refused instead.
	ErrSettingConflict = errors.New("pi settings: path is occupied by a non-object value")
)

// Pi agent settings (~/.pi/agent/settings.json) read/write with dotted
// paths ("terminal.showImages"). Mirrors pi's own keys so /settings has
// parity with stock pi: file-backed rows write here, then pitago respawns
// the pi child to pick them up (same as the /login reconnect flow).

// PiAgentDir mirrors pi's getAgentDir: $PI_CODING_AGENT_DIR or ~/.pi/agent.
func PiAgentDir() string {
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

// PiSettingsPath is <agentDir>/settings.json ("" when unresolvable).
func PiSettingsPath() string {
	if d := PiAgentDir(); d != "" {
		return filepath.Join(d, "settings.json")
	}
	return ""
}

// ReadPiSettings loads settings.json (nil when missing/unparseable).
func ReadPiSettings() map[string]any {
	return ReadPiSettingsAt(PiSettingsPath())
}

// ReadPiSettingsAt loads one settings file (nil when missing/unparseable).
func ReadPiSettingsAt(path string) map[string]any {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var v map[string]any
	if err := json.Unmarshal(stripPiBOM(raw), &v); err != nil {
		return nil
	}
	return v
}

// GetPiSetting reads a dotted path ("terminal.showImages") from cfg.
func GetPiSetting(cfg map[string]any, path string) any {
	var cur any = cfg
	for _, k := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[k]
		if !ok {
			return nil
		}
	}
	return cur
}

// piPersistedSettings are the settings.json paths pi's own SettingsManager
// already writes when the matching RPC lands: set_steering_mode,
// set_follow_up_mode, set_auto_compaction and set_auto_retry all queue a
// field-merged save themselves (verified on pi 0.87.1). A second writer for
// the same key only races pi's save and can lose the other side's edit, so
// those writes are dropped here. The RPC is the single source of truth; the
// /settings rows stay live through it and persistent through pi.
var piPersistedSettings = map[string]bool{
	"steeringMode":       true,
	"followUpMode":       true,
	"compaction.enabled": true,
	"retry.enabled":      true,
}

// PiPersistsSetting reports whether pi itself persists this settings.json
// path (see piPersistedSettings). Exported so callers can explain a no-op
// write instead of wondering why their toggle "did not stick".
func PiPersistsSetting(path string) bool { return piPersistedSettings[path] }

// SetPiSetting writes one dotted path into settings.json, creating nested
// objects as needed and preserving the file's existing keys, their order and
// the file's permissions (0644 for a new file, like pi).
//
// The write is atomic (temp file + rename) because pi owns the same file and
// reads it on every start: a half-written settings.json would drop the
// user's pi configuration. Paths pi persists itself (piPersistedSettings)
// are skipped — see above.
//
// Two situations are REFUSED instead of "helpfully" written, because
// settings.json is pi's own configuration (defaultProvider, defaultModel,
// defaultThinkingLevel, packages, theme) and losing it costs the user far
// more than any pitago UI bug:
//
//   - the file exists but does not parse (ErrUnreadableSettings): the old
//     behaviour started from an empty object and replaced the file, i.e. one
//     unparseable byte (a stray comma, a comment, a half-finished editor
//     backup) wiped the user's whole pi configuration;
//   - an intermediate key holds a non-object (ErrSettingConflict).
func SetPiSetting(path string, val any) error {
	return SetPiSettingAt(PiSettingsPath(), path, val)
}

// SetPiSettingAt writes one dotted path into the given settings file.
func SetPiSettingAt(file, path string, val any) error {
	if file == "" {
		return os.ErrNotExist
	}
	if piPersistedSettings[path] {
		return nil // pi saves this one itself; a second writer only races it
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	keys := strings.Split(path, ".")
	perm := filePerm(file, 0o644)
	for attempt := 0; attempt < settingsWriteAttempts; attempt++ {
		orig, root, err := readSettingsForWrite(file)
		if err != nil {
			return err
		}
		// Bottom-up: the nested object is encoded into its parent only after the
		// leaf below it exists, so no half-built subtree ever lands in the file.
		if err := root.setPath(keys, raw); err != nil {
			return err
		}
		out, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return err
		}
		// Keep a leading BOM: pi strips it before parsing, so the file
		// stays exactly as readable for pi as it was before, and the
		// compare-and-swap below keeps working on retries.
		if len(orig) > 0 && bytes.HasPrefix(orig, []byte(piBOM)) {
			out = append([]byte(piBOM), out...)
		}
		// The swap is conditional on the file still holding what we read:
		// pi may have saved its own change in between, and renaming over it
		// would silently drop that change.
		swapped, err := writeFileAtomicCAS(file, orig, append(out, '\n'), perm)
		if err != nil {
			return err
		}
		if swapped {
			return nil
		}
		// pi won the race: re-read and re-apply the same leaf on top of
		// pi's newer file, so the change lands without losing pi's.
	}
	return fmt.Errorf("pi settings: %s keeps changing underneath the write; nothing was written", file)
}

// readSettingsForWrite loads the file pitago is about to rewrite, returning
// the exact bytes it read (for the compare-and-swap) and the parsed object.
//
// A missing file yields an empty object (the write creates it) and an empty
// one does too (there is nothing to lose). Anything else that does not parse
// as a JSON object is an error: the caller must not turn the user's pi
// configuration into an empty file.
func readSettingsForWrite(file string) ([]byte, *orderedObject, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newOrderedObject(), nil
		}
		return nil, nil, err // unreadable (permissions, a directory): do not touch it
	}
	if len(bytes.TrimSpace(stripPiBOM(raw))) == 0 {
		// Empty, or nothing but a BOM (what a crashed editor leaves):
		// there is no content to lose, so the write may replace it.
		return raw, newOrderedObject(), nil
	}
	o := newOrderedObject()
	// Parsed with the BOM stripped (pi does the same); the untouched raw
	// bytes above stay the compare-and-swap reference.
	if err := o.UnmarshalJSON(stripPiBOM(raw)); err != nil {
		return nil, nil, fmt.Errorf("%w (%v): %s was left exactly as it is", ErrUnreadableSettings, err, file)
	}
	return raw, o, nil
}

// newOrderedObject is an empty order-preserving JSON object.
func newOrderedObject() *orderedObject {
	return &orderedObject{vals: map[string]json.RawMessage{}}
}

// setPath writes val at a dotted key path, creating the missing objects on
// the way down. A JSON null on the way down counts as missing. A non-object
// there is a conflict and aborts the write (ErrSettingConflict): replacing
// it would silently rewrite a key the user (or pi) put there.
func (o *orderedObject) setPath(keys []string, val json.RawMessage) error {
	if len(keys) == 0 {
		return fmt.Errorf("settings: empty key path")
	}
	if len(keys) == 1 {
		o.set(keys[0], val)
		return nil
	}
	next := newOrderedObject()
	if raw, ok := o.get(keys[0]); ok && !isJSONNull(raw) {
		if err := next.UnmarshalJSON(raw); err != nil {
			return fmt.Errorf("%w: %q cannot hold the nested key %q",
				ErrSettingConflict, keys[0], strings.Join(keys[1:], "."))
		}
	}
	if err := next.setPath(keys[1:], val); err != nil {
		return err
	}
	return o.setObject(keys[0], next)
}

// isJSONNull reports whether raw is the JSON literal null (an absent value).
func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// orderedObject is a JSON object that remembers key order and keeps every
// value as raw bytes. Go maps would sort keys on marshal, so every pitago
// write reshuffled the whole file — noisy diffs, and it dropped the
// formatting of keys pitago never touches. With raw values, a write changes
// exactly the leaf it was asked to change.
type orderedObject struct {
	keys []string
	vals map[string]json.RawMessage
}

// set stores a leaf value, appending the key on first sight so new keys
// land at the end instead of being sorted into the middle.
func (o *orderedObject) set(key string, val json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = val
}

// setObject stores a nested object under key (encoding it first).
func (o *orderedObject) setObject(key string, val *orderedObject) error {
	raw, err := val.MarshalJSON()
	if err != nil {
		return err
	}
	o.set(key, raw)
	return nil
}

func (o *orderedObject) get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// UnmarshalJSON decodes an object, keeping declaration order.
func (o *orderedObject) UnmarshalJSON(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("settings: not a JSON object")
	}
	o.keys = nil
	o.vals = map[string]json.RawMessage{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		k, ok := kt.(string)
		if !ok {
			return fmt.Errorf("settings: bad object key")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
		o.set(k, v)
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return err
	}
	// Nothing may follow the object: a settings.json with trailing junk is
	// not a document we can safely rewrite (rewriting it would delete the
	// junk too, and pi would not read it back the same way).
	if dec.More() {
		return fmt.Errorf("settings: trailing data after the JSON object")
	}
	return nil
}

// MarshalJSON re-emits the object in its original order.
func (o *orderedObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// PiBool reads a bool with pi's default when absent/wrong type.
func PiBool(cfg map[string]any, path string, def bool) bool {
	if v, ok := GetPiSetting(cfg, path).(bool); ok {
		return v
	}
	return def
}

// PiString reads a string with pi's default when absent/wrong type.
func PiString(cfg map[string]any, path string, def string) string {
	if v, ok := GetPiSetting(cfg, path).(string); ok && v != "" {
		return v
	}
	return def
}

// PiInt reads a number (JSON float64) with pi's default when absent.
func PiInt(cfg map[string]any, path string, def int) int {
	switch v := GetPiSetting(cfg, path).(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}
