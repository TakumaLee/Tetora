package main

// TrayManager handles system tray icon and menu.
type TrayManager struct {
	app     *App
	cfg     TrayConfig
	running bool
}

func NewTrayManager(app *App, cfg TrayConfig) *TrayManager {
	return &TrayManager{app: app, cfg: cfg}
}

// Start initializes the system tray.
// Requires Wails v3 systray package for actual implementation.
func (t *TrayManager) Start() error {
	if !t.cfg.Enabled {
		return nil
	}
	t.running = true
	return nil
}

// Stop removes the system tray icon.
func (t *TrayManager) Stop() {
	t.running = false
}

// UpdateStatus changes the tray icon based on daemon status.
func (t *TrayManager) UpdateStatus(status string) {
	_ = status
}

// TrayMenuItem represents a menu item in the system tray.
type TrayMenuItem struct {
	Label   string
	Action  string // "status", "quick", "dashboard", "quit"
	Enabled bool
}

// BuildMenu creates the tray context menu.
func (t *TrayManager) BuildMenu() []TrayMenuItem {
	items := []TrayMenuItem{
		{Label: "Open Dashboard", Action: "dashboard", Enabled: true},
	}
	for _, action := range t.cfg.QuickActions {
		items = append(items, TrayMenuItem{
			Label:   "Quick: " + action,
			Action:  action,
			Enabled: true,
		})
	}
	items = append(items,
		TrayMenuItem{Label: "---", Action: "separator", Enabled: false},
		TrayMenuItem{Label: "Quit", Action: "quit", Enabled: true},
	)
	return items
}
