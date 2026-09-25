// pitago entry: spawn pi --mode rpc, wire app + builtin + extension, run.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/app"
	"pitago/src/builtin"
	"pitago/src/components/theme"
	"pitago/src/pimark"
	"pitago/src/pirpc"
	"pitago/src/update"
)

// Set at build time: go build -ldflags "-X main.version=v0.0.1" ./src
var version = "dev"

func main() {
	cont := flag.Bool("c", false, "resume the most recent pi session")
	dirFlag := flag.String("cwd", "", "working directory for the pi session (or pass it as the first argument)")
	provider := flag.String("provider", "", "pi provider (default from ~/.pi)")
	modelFlag := flag.String("model", "", "pi model (default from ~/.pi)")
	noSession := flag.Bool("no-session", false, "don't persist session")
	mouse := flag.Bool("mouse", true, "mouse support (click sidebar, wheel scroll); --mouse=false keeps native text selection")
	themeFlag := flag.String("theme", "", "TUI theme: one-dark, gruvbox, catppuccin-mocha… (/theme lists all)")
	showVersion := flag.Bool("version", false, "print version and exit")
	doUpdate := flag.Bool("update", false, "self-update to the latest GitHub release and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("pitago " + version)
		return
	}

	if *doUpdate {
		runUpdate(version)
		return
	}

	// Two-way key sync with pi (auth.json wins over env inside pi):
	// 1. push pitago actives where pi has nothing yet so pitago-added keys
	//    reach pi; 2. import pi → pitago (union, never moves active) so keys
	//    added via stock `pi` show up here; 3. export actives to env.
	keyPath := pirpc.KeyPath()
	pirpc.EnsurePiHasActive(keyPath)
	pirpc.SyncFromPi(keyPath)
	pirpc.SyncAuthStateFromPi(pirpc.AuthStatePath())
	for env, key := range pirpc.LoadKeys(keyPath) {
		if key != "" && os.Getenv(env) == "" {
			_ = os.Setenv(env, key)
		}
	}

	cwd, err := resolveDir(*dirFlag, flag.Args())
	if err != nil {
		fmt.Println("pitago:", err)
		os.Exit(1)
	}
	opts := pirpc.Options{
		Provider: *provider, Model: *modelFlag,
		Continue: *cont, NoSession: *noSession, Dir: cwd,
	}
	pi, err := pirpc.Spawn(opts)
	if err != nil {
		fmt.Println("Cannot start pi:", err)
		os.Exit(1)
	}
	defer pi.Close()
	defer pimark.Close()
	if os.Getenv("PITAGO_RENDER") == "pi" {
		pimark.Prewarm() // warm pi's render bridge so first message isn't slow
	}

	m := app.New(pi, cwd)
	m.AppVersion = version
	// --theme wins over the saved theme: pre-save so Configure() loads it.
	if *themeFlag != "" {
		_ = theme.Save(theme.ThemePath(), *themeFlag)
	}
	m.Configure(opts, keyPath)
	// Restore the last explicitly chosen model when no --provider/--model
	// flags: pi spawns on its own default, pitago re-applies the saved
	// choice (persisted on every model switch) before the first frame.
	if *provider == "" && *modelFlag == "" {
		m.ApplySavedModel()
	}
	// Preload the update cache so the welcome banner ("⬆ … pitago
	// --update") shows on the first frame — the async auto-check in
	// Init() refreshes it right after.
	if c := update.LoadCache(update.CachePath()); update.Fresh(c, update.CheckTTL) &&
		update.NeedsUpdate(version, c.LatestTag) && update.IsRelease(version) {
		m.UpdateAvail = c.LatestTag
	}
	m.Mouse = *mouse
	m.UseBuiltins(builtin.All(), builtin.Confirmers())
	// Mouse capture on by default so the sidebar is clickable + scrollable.
	// Opt out with --mouse=false for plain highlight-to-copy.
	progOpts := []tea.ProgramOption{tea.WithAltScreen()}
	if *mouse {
		progOpts = append(progOpts, tea.WithMouseCellMotion())
	}
	prog := tea.NewProgram(m, progOpts...)
	app.ProgRef = prog
	app.WireClient(pi)
	if _, err := prog.Run(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}

// runUpdate checks the latest GitHub release and replaces this binary.
// Failures print a copy-paste fallback instead of a stack trace.
func runUpdate(current string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	latest, err := update.FetchLatest(ctx)
	if err != nil {
		fmt.Println("pitago: update check failed:", err)
		fmt.Println("hint: both github.com + api.github.com timed out → máy không ra được GitHub trực tiếp.")
		fmt.Println("  1. kiểm tra proxy: env | grep -i proxy  vs  sudo env | grep -i proxy")
		fmt.Println("  2. thử lại giữ proxy: sudo -E pitago --update")
		fmt.Println("  3. hoặc cài tay: " + update.Manual(update.CurrentAsset()))
		os.Exit(1)
	}
	if !update.NeedsUpdate(current, latest) {
		fmt.Println("pitago: already on latest (" + current + ")")
		return
	}
	fmt.Printf("pitago: updating %s → %s…\n", current, latest)
	asset := update.CurrentAsset()
	dctx, dcancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer dcancel()
	if err := update.Install(dctx, update.LatestURL(asset)); err != nil {
		if errors.Is(err, update.ErrNeedSudo) {
			fmt.Println("pitago: binary is in a system dir (/usr/local/bin) — move it to ~/.local/bin once, then no sudo ever:")
			fmt.Println("  " + update.Manual(asset))
			os.Exit(1)
		}
		fmt.Println("pitago: update failed:", err)
		fmt.Println("fallback: " + update.Manual(asset))
		os.Exit(1)
	}
	fmt.Println("pitago: updated to " + latest + " — restart to use it")
}

// resolveDir picks the session working directory: --cwd, else the first
// positional argument, else the current directory. Relative paths resolve
// against where pitago was launched; ~ expands to $HOME.
func resolveDir(flagDir string, args []string) (string, error) {
	dir := flagDir
	if dir == "" && len(args) > 0 {
		dir = args[0]
	}
	if dir == "" {
		return os.Getwd()
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if dir == "~" {
			dir = home
		} else {
			dir = filepath.Join(home, dir[2:])
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s: not a directory", dir)
	}
	return abs, nil
}
