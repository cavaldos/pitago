package pirpc

import (
	"os"
	"strings"
	"sync"
)

// Environment hygiene for the pi child.
//
// pitago used to publish a selected API key with os.Setenv / os.Unsetenv.
// That mutates pitago's OWN process environment at launch (and again on
// every /login), which leaks the secret into every other process pitago
// ever starts, into a crash dump, and into anything reading the env later.
// It also means launching pitago is not side-effect-free: the exported
// value is inherited by the pi child whether or not the user asked for it.
//
// The overlay replaces it: the value lives in this process only, is applied
// to the pi child's env at Spawn time (see Client Spawn → piChildEnviron)
// and is invisible to os.Getenv. pitago's environment therefore stays
// exactly what the user's shell exported, so `pitago` and `pi` are spawned
// from the same environment; only the deliberate /login key push reaches
// the child.
var (
	piEnvMu   sync.Mutex
	piEnvSet  = map[string]string{} // name → value handed to the pi child
	piEnvDrop = map[string]bool{}   // name explicitly removed from the child
)

// SetPiChildEnv records an env var for the pi child only. An empty val
// removes the var from the child instead (the /logout "last key deleted"
// case, where pi must not fall back to a stale ambient value).
//
// This never touches pitago's own environment; use os.Setenv for that.
func SetPiChildEnv(name, val string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	piEnvMu.Lock()
	defer piEnvMu.Unlock()
	if val == "" {
		delete(piEnvSet, name)
		piEnvDrop[name] = true
		return
	}
	delete(piEnvDrop, name)
	piEnvSet[name] = val
}

// PiChildEnv returns the value the pi child will see for name
// (ok=false when the overlay has no opinion and the process env applies).
func PiChildEnv(name string) (string, bool) {
	piEnvMu.Lock()
	defer piEnvMu.Unlock()
	if v, ok := piEnvSet[name]; ok {
		return v, true
	}
	if piEnvDrop[name] {
		return "", true
	}
	return "", false
}

// piEnvLookup resolves name the way the pi child will: overlay first, then
// the process environment. Used for auth.json "$ENV" indirection, so a
// key pushed by /login resolves exactly as it does inside pi.
func piEnvLookup(name string) string {
	if v, ok := PiChildEnv(name); ok {
		return v
	}
	return os.Getenv(name)
}

// piChildEnviron builds the pi child's environment: the process env plus
// the overlay, with dropped names removed. It returns nil when the overlay
// is empty, which makes exec inherit the environment verbatim — byte for
// byte what launching `pi` from the same shell would give it.
func piChildEnviron() []string {
	piEnvMu.Lock()
	defer piEnvMu.Unlock()
	if len(piEnvSet) == 0 && len(piEnvDrop) == 0 {
		return nil
	}
	drop := make(map[string]bool, len(piEnvDrop))
	for k := range piEnvDrop {
		drop[k] = true
	}
	env := make([]string, 0, len(os.Environ())+len(piEnvSet))
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 && drop[kv[:i]] {
			continue
		}
		if i := strings.IndexByte(kv, '='); i > 0 {
			if _, ok := piEnvSet[kv[:i]]; ok {
				continue // replaced below, in overlay order
			}
		}
		env = append(env, kv)
	}
	for k, v := range piEnvSet {
		env = append(env, k+"="+v)
	}
	return env
}

// clearPiChildEnv drops the whole overlay. Tests use it to undo a push.
func clearPiChildEnv() {
	piEnvMu.Lock()
	defer piEnvMu.Unlock()
	piEnvSet = map[string]string{}
	piEnvDrop = map[string]bool{}
}
