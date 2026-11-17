package ev

// MenuItem is one row of a context menu.
type MenuItem struct {
	ID        string
	Label     string
	Shortcut  string
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
