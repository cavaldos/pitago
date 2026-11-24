package ev

// MenuItem is one row of a context menu.
type MenuItem struct {
	ID        string
	Label     string
	Shortcut  string
	Hint      string // dim description after the label
	Toggle    bool   // picking keeps the menu open (menu bar re-reads items)
	Disabled  bool
	Separator bool
	Submenu   []MenuItem
}

// OpenMenuMsg asks the runtime to open a context menu at screen X,Y. Tag
// identifies the menu to the model; the choice comes back as MenuResultMsg.
// Runtimes without native menus ignore it (check the capability the app was
// given before relying on it).
type OpenMenuMsg struct {
	Tag   string
	X, Y  int
	Items []MenuItem
}

// MenuResultMsg reports the item picked from the menu opened with Tag.
type MenuResultMsg struct {
	Tag string
	ID  string
}

// Menu is one top-level entry of a menu bar ("File", "Edit", …).
type Menu struct {
	Title   string
	Tooltip string // hover help for the title (runtimes that show tooltips)
	Items   []MenuItem
}

// MenuBarProvider is implemented by a model that wants a menu bar above its
// frame. The runtime calls MenuBar on its UI goroutine whenever it draws the
// bar or opens one of its menus, so labels (✓ toggles) stay current. A pick
// arrives as MenuResultMsg{Tag: MenuBarTag, ID}. An empty result hides the
// bar. Runtimes without native menus never call it.
type MenuBarProvider interface {
	MenuBar() []Menu
}

// MenuBarTag is the MenuResultMsg tag of menu bar picks.
const MenuBarTag = "menubar"
