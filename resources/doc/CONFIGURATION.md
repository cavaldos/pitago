# Configuration Files

Everything pitago stores lives under `~/.config/pitago/` (except the pi stderr log).

| File | Mode | Purpose |
| ---- | ---- | ------- |
| `~/.config/pitago/keys.json` | `0600` | Saved API keys: several per provider, one active, optional name + added date. Written by `/login` / `/logout`; the active key is mirrored into pi's `auth.json` so pi sees the models. |
| `~/.config/pitago/pi_auth.json` | `0600` | Mirrored pi logins (OAuth account + expiry, no secrets) so `/login` lists and disconnects subscriptions made in stock pi. |
| `~/.config/pitago/recent_models.json` | — | Recent models, max 5 (`Ctrl+R`). |
| `~/.config/pitago/prefs.json` | `0600` | Display prefs + `currentModel`, the last model you picked. Passed to `pi` as `--provider/--model` at startup so a new window reopens on it (explicit flags win). Also holds the sidebar pet (`pet`, missing = `cat`) and its look (`petStyle`: `ascii` default, or `classic`), and `taskWidgetOff: true` to hide the above-editor task widget. |
| `~/.config/pitago/theme.json` | — | Active TUI theme. 25 built-ins: default, one-dark, gruvbox, catppuccin-mocha, dracula… `/theme` lists all. |
| `~/.config/pitago/update.json` | — | Last update check (timestamp + tag), 24h TTL. |
| `/tmp/pitago-pi-stderr.log` | — | Stderr of the pi child process. |

Delete `~/.config/pitago` to reset everything (keys, recent models, prefs, theme).