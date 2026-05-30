package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	_ "github.com/go-sql-driver/mysql"
)

var errBack = errors.New("back")

// ── types ────────────────────────────────────────────────────────────────────

type dbConfig struct {
	Name     string
	Host     string
	Port     string
	User     string
	Password string
	Database string // populated after connecting, not from user input
}

func (c dbConfig) label() string {
	if c.Name != "" {
		return fmt.Sprintf("[%s] %s.%%s", c.Name, c.Database)
	}
	return fmt.Sprintf("%s.%%s", c.Database)
}

func (c dbConfig) serverDSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/?parseTime=true",
		c.User, c.Password, c.Host, c.Port)
}

// ── saved connections ─────────────────────────────────────────────────────────

type savedConn struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mysql-copy"), nil
}

func loadConnections() ([]savedConn, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "connections.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var conns []savedConn
	return conns, json.Unmarshal(data, &conns)
}

func saveConnection(conn savedConn) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	conns, _ := loadConnections()
	replaced := false
	for i, c := range conns {
		if c.Name == conn.Name {
			conns[i] = conn
			replaced = true
			break
		}
	}
	if !replaced {
		conns = append(conns, conn)
	}
	data, err := json.MarshalIndent(conns, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "connections.json"), data, 0600)
}

func findConnection(name string) (savedConn, bool) {
	conns, _ := loadConnections()
	for _, c := range conns {
		if c.Name == name {
			return c, true
		}
	}
	return savedConn{}, false
}

// ── presets ───────────────────────────────────────────────────────────────────

type preset struct {
	Name          string `json:"name"`
	SrcConnection string `json:"src_connection"`
	SrcDatabase   string `json:"src_database"`
	SrcTable      string `json:"src_table"`
	DstConnection string `json:"dst_connection"`
	DstDatabase   string `json:"dst_database"`
	DstTable      string `json:"dst_table"`
	Truncate      bool   `json:"truncate"`
}

func (p preset) summary() string {
	suffix := ""
	if p.Truncate {
		suffix = " (truncate)"
	}
	return fmt.Sprintf("[%s] %s.%s  →  [%s] %s.%s%s",
		p.SrcConnection, p.SrcDatabase, p.SrcTable,
		p.DstConnection, p.DstDatabase, p.DstTable, suffix)
}

func loadPresets() ([]preset, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "presets.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ps []preset
	return ps, json.Unmarshal(data, &ps)
}

func savePreset(p preset) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	ps, _ := loadPresets()
	replaced := false
	for i, existing := range ps {
		if existing.Name == p.Name {
			ps[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "presets.json"), data, 0600)
}

func findPreset(name string) (preset, bool) {
	ps, _ := loadPresets()
	for _, p := range ps {
		if p.Name == name {
			return p, true
		}
	}
	return preset{}, false
}

// ── groups ────────────────────────────────────────────────────────────────────

type group struct {
	Name        string   `json:"name"`
	Concurrency int      `json:"concurrency"`
	Presets     []string `json:"presets"`
}

func (g group) summary() string {
	parallel := "sequential"
	if g.Concurrency > 1 {
		parallel = fmt.Sprintf("%d parallel", g.Concurrency)
	}
	return fmt.Sprintf("%d presets · %s", len(g.Presets), parallel)
}

func loadGroups() ([]group, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "groups.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var gs []group
	return gs, json.Unmarshal(data, &gs)
}

func saveGroup(g group) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	gs, _ := loadGroups()
	replaced := false
	for i, existing := range gs {
		if existing.Name == g.Name {
			gs[i] = g
			replaced = true
			break
		}
	}
	if !replaced {
		gs = append(gs, g)
	}
	data, err := json.MarshalIndent(gs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "groups.json"), data, 0600)
}

func deleteGroup(name string) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	gs, _ := loadGroups()
	filtered := gs[:0]
	for _, g := range gs {
		if g.Name != name {
			filtered = append(filtered, g)
		}
	}
	data, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "groups.json"), data, 0600)
}

// ── connection prompts ────────────────────────────────────────────────────────

const (
	newConnOption = "Enter new connection"
	backOption    = "← Back"
)

func promptConnection() (dbConfig, bool, error) {
	conns, _ := loadConnections()

	if len(conns) > 0 {
		options := make([]string, 0, len(conns)+1)
		for _, c := range conns {
			options = append(options, c.Name)
		}
		options = append(options, newConnOption)

		var choice string
		if err := survey.AskOne(&survey.Select{
			Message: "Use a saved connection or enter new?",
			Options: options,
		}, &choice); err != nil {
			return dbConfig{}, false, err
		}

		if choice != newConnOption {
			for _, c := range conns {
				if c.Name == choice {
					return dbConfig{
						Name:     c.Name,
						Host:     c.Host,
						Port:     c.Port,
						User:     c.User,
						Password: c.Password,
					}, false, nil
				}
			}
		}
	}

	cfg, err := promptNewConnection()
	return cfg, true, err
}

func promptNewConnection() (dbConfig, error) {
	answers := struct {
		Host     string
		Port     string
		User     string
		Password string
	}{}
	err := survey.Ask([]*survey.Question{
		{Name: "host", Prompt: &survey.Input{Message: "Host:", Default: "localhost"}},
		{Name: "port", Prompt: &survey.Input{Message: "Port:", Default: "3306"}},
		{Name: "user", Prompt: &survey.Input{Message: "User:"}},
		{Name: "password", Prompt: &survey.Password{Message: "Password:"}},
	}, &answers)
	return dbConfig{
		Host:     answers.Host,
		Port:     answers.Port,
		User:     answers.User,
		Password: answers.Password,
	}, err
}

func offerSave(cfg dbConfig) string {
	var save bool
	if err := survey.AskOne(&survey.Confirm{Message: "Save this connection for future use?"}, &save); err != nil || !save {
		return ""
	}
	var name string
	if err := survey.AskOne(&survey.Input{Message: "Connection name:"}, &name); err != nil || name == "" {
		return ""
	}
	if err := saveConnection(savedConn{
		Name: name, Host: cfg.Host, Port: cfg.Port,
		User: cfg.User, Password: cfg.Password,
	}); err != nil {
		fmt.Printf("  Warning: could not save connection: %v\n", err)
		return ""
	}
	fmt.Printf("  Saved as %q\n", name)
	return name
}

func ensureNamed(cfg dbConfig) dbConfig {
	if cfg.Name != "" {
		return cfg
	}
	fmt.Printf("  Connection [%s@%s] needs a name to be stored in a preset.\n", cfg.User, cfg.Host)
	var name string
	if err := survey.AskOne(&survey.Input{Message: "  Connection name:"}, &name); err != nil || name == "" {
		return cfg
	}
	if err := saveConnection(savedConn{
		Name: name, Host: cfg.Host, Port: cfg.Port,
		User: cfg.User, Password: cfg.Password,
	}); err != nil {
		fmt.Printf("  Warning: could not save connection: %v\n", err)
		return cfg
	}
	cfg.Name = name
	return cfg
}

func offerSavePreset(srcCfg dbConfig, srcTable string, dstCfg dbConfig, dstTable string, truncate bool) {
	var save bool
	if err := survey.AskOne(&survey.Confirm{Message: "Save this as a preset for future reuse?"}, &save); err != nil || !save {
		return
	}
	srcCfg = ensureNamed(srcCfg)
	dstCfg = ensureNamed(dstCfg)
	if srcCfg.Name == "" || dstCfg.Name == "" {
		fmt.Println("  Preset not saved (connections need names).")
		return
	}
	var name string
	if err := survey.AskOne(&survey.Input{Message: "Preset name:"}, &name); err != nil || name == "" {
		return
	}
	if err := savePreset(preset{
		Name:          name,
		SrcConnection: srcCfg.Name, SrcDatabase: srcCfg.Database, SrcTable: srcTable,
		DstConnection: dstCfg.Name, DstDatabase: dstCfg.Database, DstTable: dstTable,
		Truncate: truncate,
	}); err != nil {
		fmt.Printf("  Warning: could not save preset: %v\n", err)
		return
	}
	fmt.Printf("  Preset saved as %q\n", name)
}

// ── db helpers ────────────────────────────────────────────────────────────────

func openDB(cfg dbConfig) (*sql.DB, error) {
	db, err := sql.Open("mysql", cfg.serverDSN())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func pickDatabase(db *sql.DB) (string, error) {
	rows, err := db.Query("SHOW DATABASES")
	if err != nil {
		return "", fmt.Errorf("listing databases: %w", err)
	}
	defer rows.Close()
	var databases []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return "", err
		}
		databases = append(databases, d)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(databases) == 0 {
		return "", fmt.Errorf("no databases found")
	}
	options := append([]string{backOption}, databases...)
	var choice string
	if err := survey.AskOne(&survey.Select{Message: "Select database:", Options: options}, &choice); err != nil {
		return "", err
	}
	if choice == backOption {
		return "", errBack
	}
	if _, err := db.Exec(fmt.Sprintf("USE `%s`", choice)); err != nil {
		return "", fmt.Errorf("switching to database: %w", err)
	}
	return choice, nil
}

func listTables(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func pickTable(db *sql.DB, prompt string) (string, error) {
	tables, err := listTables(db)
	if err != nil {
		return "", fmt.Errorf("listing tables: %w", err)
	}
	if len(tables) == 0 {
		return "", fmt.Errorf("no tables found in database")
	}
	options := append([]string{backOption}, tables...)
	var choice string
	if err := survey.AskOne(&survey.Select{Message: prompt, Options: options}, &choice); err != nil {
		return "", err
	}
	if choice == backOption {
		return "", errBack
	}
	return choice, nil
}

func setupSide(label string) (*sql.DB, dbConfig, string, error) {
connLoop:
	for {
		fmt.Printf("%s database:\n", label)
		cfg, isNew, err := promptConnection()
		if err != nil {
			return nil, dbConfig{}, "", err
		}
		fmt.Print("Testing connection... ")
		db, err := openDB(cfg)
		if err != nil {
			fmt.Printf("FAILED\n  %v\n\n", err)
			continue connLoop
		}
		fmt.Println("OK")
		if isNew {
			cfg.Name = offerSave(cfg)
		}
		for {
			database, err := pickDatabase(db)
			if errors.Is(err, errBack) {
				db.Close()
				fmt.Println()
				continue connLoop
			}
			if err != nil {
				return nil, dbConfig{}, "", err
			}
			cfg.Database = database
			for {
				table, err := pickTable(db, fmt.Sprintf("Select %s table:", strings.ToLower(label)))
				if errors.Is(err, errBack) {
					break
				}
				if err != nil {
					return nil, dbConfig{}, "", err
				}
				return db, cfg, table, nil
			}
		}
	}
}

// ── copy ──────────────────────────────────────────────────────────────────────

func truncateTable(db *sql.DB, table string) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquiring connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return fmt.Errorf("disabling foreign key checks: %w", err)
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE `%s`", table)); err != nil {
		conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1") // best-effort re-enable
		return fmt.Errorf("truncating table: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		return fmt.Errorf("re-enabling foreign key checks: %w", err)
	}
	return nil
}

func copyTable(srcDB *sql.DB, srcTable string, dstDB *sql.DB, dstTable string) error {
	colRows, err := srcDB.Query(fmt.Sprintf("SELECT * FROM `%s` LIMIT 0", srcTable))
	if err != nil {
		return fmt.Errorf("reading source columns: %w", err)
	}
	cols, err := colRows.Columns()
	colRows.Close()
	if err != nil {
		return err
	}

	colList := "`" + strings.Join(cols, "`,`") + "`"
	placeholder := "(" + strings.Repeat("?,", len(cols)-1) + "?)"

	srcRows, err := srcDB.Query(fmt.Sprintf("SELECT * FROM `%s`", srcTable))
	if err != nil {
		return fmt.Errorf("reading source rows: %w", err)
	}
	defer srcRows.Close()

	const batchSize = 500
	var (
		batch    [][]interface{}
		total    int
		scanArgs = make([]interface{}, len(cols))
		vals     = make([]interface{}, len(cols))
	)
	for i := range vals {
		scanArgs[i] = &vals[i]
	}

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		phs := make([]string, len(batch))
		args := make([]interface{}, 0, len(batch)*len(cols))
		for i, row := range batch {
			phs[i] = placeholder
			args = append(args, row...)
		}
		_, err := dstDB.Exec(
			fmt.Sprintf("INSERT INTO `%s` (%s) VALUES %s", dstTable, colList, strings.Join(phs, ",")),
			args...)
		return err
	}

	for srcRows.Next() {
		if err := srcRows.Scan(scanArgs...); err != nil {
			return err
		}
		row := make([]interface{}, len(cols))
		copy(row, vals)
		batch = append(batch, row)
		total++
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return fmt.Errorf("inserting batch at row %d: %w", total, err)
			}
			fmt.Printf("\r    %d rows copied...", total)
			batch = batch[:0]
		}
	}
	if err := srcRows.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return fmt.Errorf("inserting final batch: %w", err)
	}
	if total == 0 {
		fmt.Println("    source table is empty, nothing to copy.")
	} else {
		fmt.Printf("\r    %d rows copied.          \n", total)
	}
	return nil
}

// ── preset execution ──────────────────────────────────────────────────────────

// execPreset connects and copies without prompting. Used by both runPreset and runGroup.
func execPreset(p preset) error {
	srcSaved, ok := findConnection(p.SrcConnection)
	if !ok {
		return fmt.Errorf("saved connection %q not found", p.SrcConnection)
	}
	dstSaved, ok := findConnection(p.DstConnection)
	if !ok {
		return fmt.Errorf("saved connection %q not found", p.DstConnection)
	}

	fmt.Print("  Connecting to source... ")
	srcDB, err := openDB(dbConfig{Host: srcSaved.Host, Port: srcSaved.Port, User: srcSaved.User, Password: srcSaved.Password})
	if err != nil {
		return fmt.Errorf("source connection failed: %w", err)
	}
	defer srcDB.Close()
	if _, err := srcDB.Exec(fmt.Sprintf("USE `%s`", p.SrcDatabase)); err != nil {
		return fmt.Errorf("USE %s: %w", p.SrcDatabase, err)
	}
	fmt.Println("OK")

	fmt.Print("  Connecting to destination... ")
	dstDB, err := openDB(dbConfig{Host: dstSaved.Host, Port: dstSaved.Port, User: dstSaved.User, Password: dstSaved.Password})
	if err != nil {
		return fmt.Errorf("destination connection failed: %w", err)
	}
	defer dstDB.Close()
	if _, err := dstDB.Exec(fmt.Sprintf("USE `%s`", p.DstDatabase)); err != nil {
		return fmt.Errorf("USE %s: %w", p.DstDatabase, err)
	}
	fmt.Println("OK")

	if p.Truncate {
		fmt.Printf("  Truncating %s.%s...\n", p.DstDatabase, p.DstTable)
		if err := truncateTable(dstDB, p.DstTable); err != nil {
			return err
		}
	}

	fmt.Println("  Copying...")
	return copyTable(srcDB, p.SrcTable, dstDB, p.DstTable)
}

// runPreset shows the preset details, asks for confirmation, then executes.
func runPreset(p preset) {
	fmt.Printf("Preset: %s\n  %s\n\n", p.Name, p.summary())

	var confirm bool
	truncateNote := ""
	if p.Truncate {
		truncateNote = " (will truncate destination first)"
	}
	if err := survey.AskOne(&survey.Confirm{
		Message: fmt.Sprintf("Copy [%s] %s.%s  →  [%s] %s.%s%s ?",
			p.SrcConnection, p.SrcDatabase, p.SrcTable,
			p.DstConnection, p.DstDatabase, p.DstTable,
			truncateNote),
	}, &confirm); err != nil || !confirm {
		fmt.Println("Cancelled.")
		return
	}

	if err := execPreset(p); err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Println("Done!")
}

// ── group management ──────────────────────────────────────────────────────────

func promptGroupForm(existing *group) (group, error) {
	allPresets, _ := loadPresets()
	if len(allPresets) == 0 {
		return group{}, fmt.Errorf("no presets available — create a preset first")
	}

	presetNames := make([]string, len(allPresets))
	for i, p := range allPresets {
		presetNames[i] = p.Name
	}

	defaultName := ""
	defaultConcurrency := "1"
	defaultSelected := []string{}
	if existing != nil {
		defaultName = existing.Name
		defaultConcurrency = fmt.Sprintf("%d", existing.Concurrency)
		defaultSelected = existing.Presets
	}

	var name string
	if err := survey.AskOne(&survey.Input{
		Message: "Group name:",
		Default: defaultName,
	}, &name); err != nil || name == "" {
		return group{}, errBack
	}

	var concurrencyStr string
	if err := survey.AskOne(&survey.Input{
		Message: "Concurrency (how many presets to run in parallel):",
		Default: defaultConcurrency,
	}, &concurrencyStr); err != nil {
		return group{}, err
	}
	concurrency := 1
	fmt.Sscanf(concurrencyStr, "%d", &concurrency)
	if concurrency < 1 {
		concurrency = 1
	}

	var selected []string
	if err := survey.AskOne(&survey.MultiSelect{
		Message: "Select presets to include:",
		Options: presetNames,
		Default: defaultSelected,
	}, &selected); err != nil {
		return group{}, err
	}
	if len(selected) == 0 {
		return group{}, fmt.Errorf("a group must have at least one preset")
	}

	return group{Name: name, Concurrency: concurrency, Presets: selected}, nil
}

func manageGroups() {
	for {
		gs, _ := loadGroups()

		options := []string{"Create new group"}
		for _, g := range gs {
			options = append(options, fmt.Sprintf("%-20s  (%s)", g.Name, g.summary()))
		}
		options = append(options, backOption)

		var choice string
		if err := survey.AskOne(&survey.Select{
			Message: "Manage groups:",
			Options: options,
		}, &choice); err != nil || choice == backOption {
			return
		}

		if choice == "Create new group" {
			g, err := promptGroupForm(nil)
			if err != nil {
				if !errors.Is(err, errBack) {
					fmt.Printf("  error: %v\n", err)
				}
				continue
			}
			if err := saveGroup(g); err != nil {
				fmt.Printf("  error saving group: %v\n", err)
				continue
			}
			fmt.Printf("  Group %q saved.\n\n", g.Name)
			continue
		}

		// A group was selected — show edit/delete submenu.
		// Extract the group name (everything before the padding).
		selectedName := strings.TrimSpace(strings.SplitN(choice, " ", 2)[0])
		var selectedGroup group
		for _, g := range gs {
			if g.Name == selectedName {
				selectedGroup = g
				break
			}
		}

		var action string
		if err := survey.AskOne(&survey.Select{
			Message: fmt.Sprintf("%q:", selectedGroup.Name),
			Options: []string{"Edit", "Delete", backOption},
		}, &action); err != nil || action == backOption {
			continue
		}

		switch action {
		case "Edit":
			updated, err := promptGroupForm(&selectedGroup)
			if err != nil {
				if !errors.Is(err, errBack) {
					fmt.Printf("  error: %v\n", err)
				}
				continue
			}
			// If the name changed, remove the old entry first.
			if updated.Name != selectedGroup.Name {
				deleteGroup(selectedGroup.Name)
			}
			if err := saveGroup(updated); err != nil {
				fmt.Printf("  error saving group: %v\n", err)
				continue
			}
			fmt.Printf("  Group %q saved.\n\n", updated.Name)

		case "Delete":
			var confirm bool
			survey.AskOne(&survey.Confirm{
				Message: fmt.Sprintf("Delete group %q?", selectedGroup.Name),
			}, &confirm)
			if confirm {
				deleteGroup(selectedGroup.Name)
				fmt.Printf("  Group %q deleted.\n\n", selectedGroup.Name)
			}
		}
	}
}

// ── run group ─────────────────────────────────────────────────────────────────

func runGroup(g group) {
	fmt.Printf("Group: %s  (%s)\n\n", g.Name, g.summary())

	// Resolve and display all presets before confirming.
	ps := make([]preset, 0, len(g.Presets))
	for _, name := range g.Presets {
		p, ok := findPreset(name)
		if !ok {
			fmt.Printf("  warning: preset %q not found, skipping\n", name)
			continue
		}
		truncNote := ""
		if p.Truncate {
			truncNote = "  (truncate)"
		}
		fmt.Printf("  • %-20s  %s\n", p.Name, p.summary()+truncNote)
		ps = append(ps, p)
	}
	if len(ps) == 0 {
		fmt.Println("No valid presets in this group.")
		return
	}

	fmt.Println()
	var confirm bool
	if err := survey.AskOne(&survey.Confirm{
		Message: fmt.Sprintf("Run %d preset(s)?", len(ps)),
	}, &confirm); err != nil || !confirm {
		fmt.Println("Cancelled.")
		return
	}
	fmt.Println()

	for i, p := range ps {
		fmt.Printf("[%d/%d] %s\n", i+1, len(ps), p.Name)
		if err := execPreset(p); err != nil {
			fmt.Printf("  error: %v\n\n", err)
			continue
		}
		fmt.Printf("  Done.\n\n")
	}
	fmt.Println("Group finished.")
}

// ── view presets ─────────────────────────────────────────────────────────────

func viewPresets(ps []preset) {
	fmt.Printf("Presets (%d):\n\n", len(ps))
	for _, p := range ps {
		truncate := ""
		if p.Truncate {
			truncate = "  truncate"
		}
		fmt.Printf("  %-22s [%s] %s.%s  →  [%s] %s.%s%s\n",
			p.Name,
			p.SrcConnection, p.SrcDatabase, p.SrcTable,
			p.DstConnection, p.DstDatabase, p.DstTable,
			truncate)
	}
	fmt.Println()
	survey.AskOne(&survey.Input{Message: "Press Enter to go back"}, new(string))
	fmt.Println()
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	fmt.Println("=== MySQL Table Copy ===")
	fmt.Println()

	for {
		presets, _ := loadPresets()
		groups, _ := loadGroups()

		// Build top-level menu dynamically based on what exists.
		options := []string{}
		if len(presets) > 0 {
			options = append(options, "Run a preset")
			options = append(options, "View presets")
		}
		if len(groups) > 0 {
			options = append(options, "Run a group")
		}
		if len(presets) > 0 {
			options = append(options, "Manage groups")
		}
		options = append(options, "Start new copy")
		options = append(options, "Exit")

		action := "Start new copy"
		if len(options) > 1 {
			if err := survey.AskOne(&survey.Select{
				Message: "What would you like to do?",
				Options: options,
			}, &action); err != nil {
				return
			}
		}
		fmt.Println()

		switch action {

		case "Exit":
			return

		case "Run a preset":
			optionMap := map[string]preset{}
			opts := make([]string, 0, len(presets)+1)
			for _, p := range presets {
				key := fmt.Sprintf("%-20s  %s", p.Name, p.summary())
				opts = append(opts, key)
				optionMap[key] = p
			}
			opts = append(opts, backOption)
			var choice string
			if err := survey.AskOne(&survey.Select{
				Message: "Select preset:",
				Options: opts,
			}, &choice); err != nil || choice == backOption {
				continue
			}
			if p, ok := optionMap[choice]; ok {
				fmt.Println()
				runPreset(p)
			}

		case "View presets":
			viewPresets(presets)

		case "Run a group":
			opts := make([]string, 0, len(groups)+1)
			groupMap := map[string]group{}
			for _, g := range groups {
				key := fmt.Sprintf("%-20s  (%s)", g.Name, g.summary())
				opts = append(opts, key)
				groupMap[key] = g
			}
			opts = append(opts, backOption)
			var choice string
			if err := survey.AskOne(&survey.Select{
				Message: "Select group:",
				Options: opts,
			}, &choice); err != nil || choice == backOption {
				continue
			}
			if g, ok := groupMap[choice]; ok {
				fmt.Println()
				runGroup(g)
			}

		case "Manage groups":
			manageGroups()

		case "Start new copy":
			srcDB, srcCfg, srcTable, err := setupSide("Source")
			if err != nil {
				fmt.Printf("error: %v\n", err)
				continue
			}
			defer srcDB.Close()
			fmt.Println()

			dstDB, dstCfg, dstTable, err := setupSide("Destination")
			if err != nil {
				fmt.Printf("error: %v\n", err)
				continue
			}
			defer dstDB.Close()
			fmt.Println()

			var truncate bool
			if err := survey.AskOne(&survey.Confirm{
				Message: fmt.Sprintf("Truncate destination (%s.%s) before copying?", dstCfg.Database, dstTable),
			}, &truncate); err != nil {
				continue
			}

			truncateNote := ""
			if truncate {
				truncateNote = " (will truncate destination first)"
			}
			var confirm bool
			if err := survey.AskOne(&survey.Confirm{
				Message: fmt.Sprintf("Copy %s  →  %s%s ?",
					fmt.Sprintf(srcCfg.label(), srcTable),
					fmt.Sprintf(dstCfg.label(), dstTable),
					truncateNote),
			}, &confirm); err != nil || !confirm {
				fmt.Println("Cancelled.")
				continue
			}

			offerSavePreset(srcCfg, srcTable, dstCfg, dstTable, truncate)

			if truncate {
				fmt.Printf("Truncating %s.%s...\n", dstCfg.Database, dstTable)
				if err := truncateTable(dstDB, dstTable); err != nil {
					fmt.Printf("error: %v\n", err)
					continue
				}
			}
			fmt.Println("Copying...")
			if err := copyTable(srcDB, srcTable, dstDB, dstTable); err != nil {
				fmt.Printf("error: %v\n", err)
				continue
			}
			fmt.Println("Done!")
		}
	}
}
