package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	_ "github.com/go-sql-driver/mysql"
)

var errBack = errors.New("back")

// ── types ────────────────────────────────────────────────────────────────────

type dbConfig struct {
	Name     string // empty for unsaved connections
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
	var presets []preset
	return presets, json.Unmarshal(data, &presets)
}

func savePreset(p preset) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	presets, _ := loadPresets()
	replaced := false
	for i, existing := range presets {
		if existing.Name == p.Name {
			presets[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		presets = append(presets, p)
	}

	data, err := json.MarshalIndent(presets, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "presets.json"), data, 0600)
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

// offerSave prompts to save a new connection and returns the name given (empty if skipped).
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
		Name:     name,
		Host:     cfg.Host,
		Port:     cfg.Port,
		User:     cfg.User,
		Password: cfg.Password,
	}); err != nil {
		fmt.Printf("  Warning: could not save connection: %v\n", err)
		return ""
	}
	fmt.Printf("  Saved as %q\n", name)
	return name
}

// ensureNamed ensures the config has a saved name (prompting if needed).
// Required before saving a preset, since presets reference connections by name.
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
		Name:     name,
		Host:     cfg.Host,
		Port:     cfg.Port,
		User:     cfg.User,
		Password: cfg.Password,
	}); err != nil {
		fmt.Printf("  Warning: could not save connection: %v\n", err)
		return cfg
	}
	cfg.Name = name
	return cfg
}

// offerSavePreset prompts to save the current copy config as a named preset.
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

	p := preset{
		Name:          name,
		SrcConnection: srcCfg.Name,
		SrcDatabase:   srcCfg.Database,
		SrcTable:      srcTable,
		DstConnection: dstCfg.Name,
		DstDatabase:   dstCfg.Database,
		DstTable:      dstTable,
		Truncate:      truncate,
	}
	if err := savePreset(p); err != nil {
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
	if err := survey.AskOne(&survey.Select{
		Message: "Select database:",
		Options: options,
	}, &choice); err != nil {
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
	if err := survey.AskOne(&survey.Select{
		Message: prompt,
		Options: options,
	}, &choice); err != nil {
		return "", err
	}
	if choice == backOption {
		return "", errBack
	}
	return choice, nil
}

// setupSide runs the connection → database → table flow for one side,
// looping back whenever the user picks ← Back or a connection test fails.
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
					break // re-pick database
				}
				if err != nil {
					return nil, dbConfig{}, "", err
				}
				return db, cfg, table, nil
			}
		}
	}
}

// ── run preset ────────────────────────────────────────────────────────────────

func runPreset(p preset) {
	fmt.Printf("Preset: %s\n", p.name())
	fmt.Println()

	srcSaved, ok := findConnection(p.SrcConnection)
	if !ok {
		fmt.Printf("error: saved connection %q not found — edit ~/.mysql-copy/connections.json\n", p.SrcConnection)
		return
	}
	dstSaved, ok := findConnection(p.DstConnection)
	if !ok {
		fmt.Printf("error: saved connection %q not found — edit ~/.mysql-copy/connections.json\n", p.DstConnection)
		return
	}

	fmt.Print("Connecting to source... ")
	srcDB, err := openDB(dbConfig{Host: srcSaved.Host, Port: srcSaved.Port, User: srcSaved.User, Password: srcSaved.Password})
	if err != nil {
		fmt.Printf("FAILED\n  %v\n", err)
		return
	}
	defer srcDB.Close()
	if _, err := srcDB.Exec(fmt.Sprintf("USE `%s`", p.SrcDatabase)); err != nil {
		fmt.Printf("FAILED\n  %v\n", err)
		return
	}
	fmt.Println("OK")

	fmt.Print("Connecting to destination... ")
	dstDB, err := openDB(dbConfig{Host: dstSaved.Host, Port: dstSaved.Port, User: dstSaved.User, Password: dstSaved.Password})
	if err != nil {
		fmt.Printf("FAILED\n  %v\n", err)
		return
	}
	defer dstDB.Close()
	if _, err := dstDB.Exec(fmt.Sprintf("USE `%s`", p.DstDatabase)); err != nil {
		fmt.Printf("FAILED\n  %v\n", err)
		return
	}
	fmt.Println("OK")
	fmt.Println()

	truncateNote := ""
	if p.Truncate {
		truncateNote = " (will truncate destination first)"
	}
	var confirm bool
	if err := survey.AskOne(&survey.Confirm{
		Message: fmt.Sprintf("Copy [%s] %s.%s  →  [%s] %s.%s%s ?",
			p.SrcConnection, p.SrcDatabase, p.SrcTable,
			p.DstConnection, p.DstDatabase, p.DstTable,
			truncateNote),
	}, &confirm); err != nil || !confirm {
		fmt.Println("Cancelled.")
		return
	}

	if p.Truncate {
		fmt.Printf("Truncating %s.%s...\n", p.DstDatabase, p.DstTable)
		if err := truncateTable(dstDB, p.DstTable); err != nil {
			fmt.Printf("error: %v\n", err)
			return
		}
	}

	fmt.Println("Copying...")
	if err := copyTable(srcDB, p.SrcTable, dstDB, p.DstTable); err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Println("Done!")
}

func (p preset) name() string { return p.Name }

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
		query := fmt.Sprintf("INSERT INTO `%s` (%s) VALUES %s", dstTable, colList, strings.Join(phs, ","))
		_, err := dstDB.Exec(query, args...)
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
			fmt.Printf("\r  %d rows copied...", total)
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
		fmt.Println("  Source table is empty, nothing to copy.")
	} else {
		fmt.Printf("\r  %d rows copied.          \n", total)
	}
	return nil
}

// ── main ──────────────────────────────────────────────────────────────────────

const newCopyOption = "Start new copy"

func main() {
	fmt.Println("=== MySQL Table Copy ===")
	fmt.Println()

	// If presets exist, offer to run one.
	presets, _ := loadPresets()
	if len(presets) > 0 {
		optionMap := map[string]preset{}
		options := []string{newCopyOption}
		for _, p := range presets {
			key := fmt.Sprintf("%-20s  %s", p.Name, p.summary())
			options = append(options, key)
			optionMap[key] = p
		}

		var choice string
		if err := survey.AskOne(&survey.Select{
			Message: "Run a preset or start a new copy?",
			Options: options,
		}, &choice); err != nil {
			return
		}

		if p, ok := optionMap[choice]; ok {
			fmt.Println()
			runPreset(p)
			return
		}
		fmt.Println()
	}

	// New copy flow.
	srcDB, srcCfg, srcTable, err := setupSide("Source")
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	defer srcDB.Close()

	fmt.Println()

	dstDB, dstCfg, dstTable, err := setupSide("Destination")
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	defer dstDB.Close()

	fmt.Println()

	var truncate bool
	if err := survey.AskOne(&survey.Confirm{
		Message: fmt.Sprintf("Truncate destination (%s.%s) before copying?", dstCfg.Database, dstTable),
	}, &truncate); err != nil {
		return
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
		return
	}

	offerSavePreset(srcCfg, srcTable, dstCfg, dstTable, truncate)

	if truncate {
		fmt.Printf("Truncating %s.%s...\n", dstCfg.Database, dstTable)
		if err := truncateTable(dstDB, dstTable); err != nil {
			fmt.Printf("error: %v\n", err)
			return
		}
	}

	fmt.Println("Copying...")
	if err := copyTable(srcDB, srcTable, dstDB, dstTable); err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Println("Done!")
}
