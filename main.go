package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	_ "github.com/go-sql-driver/mysql"
)

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

func (c dbConfig) dsn() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		c.User, c.Password, c.Host, c.Port, c.Database)
}

func (c dbConfig) serverDSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/?parseTime=true",
		c.User, c.Password, c.Host, c.Port)
}

// --- saved connections ---

type savedConn struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

func configFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mysql-copy", "connections.json"), nil
}

func loadConnections() ([]savedConn, error) {
	path, err := configFile()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
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
	path, err := configFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	conns, _ := loadConnections()

	// replace existing entry with the same name, otherwise append
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
	// 0600 — readable only by the current user
	return os.WriteFile(path, data, 0600)
}

// --- connection prompts ---

const newConnOption = "Enter new connection"

// promptConnection shows saved connections (if any) and lets the user pick one
// or enter new details. Returns the config and whether it is newly entered.
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
		{
			Name:   "host",
			Prompt: &survey.Input{Message: "Host:", Default: "localhost"},
		},
		{
			Name:   "port",
			Prompt: &survey.Input{Message: "Port:", Default: "3306"},
		},
		{
			Name:   "user",
			Prompt: &survey.Input{Message: "User:"},
		},
		{
			Name:   "password",
			Prompt: &survey.Password{Message: "Password:"},
		},
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

// --- db helpers ---

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

	var database string
	if err := survey.AskOne(&survey.Select{
		Message: "Select database:",
		Options: databases,
	}, &database); err != nil {
		return "", err
	}

	if _, err := db.Exec(fmt.Sprintf("USE `%s`", database)); err != nil {
		return "", fmt.Errorf("switching to database: %w", err)
	}
	return database, nil
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

	var table string
	err = survey.AskOne(&survey.Select{
		Message: prompt,
		Options: tables,
	}, &table)
	return table, err
}

// --- copy ---

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

// --- main ---

func main() {
	fmt.Println("=== MySQL Table Copy ===")
	fmt.Println()

	// --- Source ---
	fmt.Println("Source database:")
	srcCfg, isNewSrc, err := promptConnection()
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	fmt.Print("Testing source connection... ")
	srcDB, err := openDB(srcCfg)
	if err != nil {
		fmt.Printf("FAILED\n  %v\n", err)
		return
	}
	defer srcDB.Close()
	fmt.Println("OK")

	if isNewSrc {
		srcCfg.Name = offerSave(srcCfg)
	}

	srcCfg.Database, err = pickDatabase(srcDB)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	srcTable, err := pickTable(srcDB, "Select source table:")
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	fmt.Println()

	// --- Destination ---
	fmt.Println("Destination database:")
	dstCfg, isNewDst, err := promptConnection()
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	fmt.Print("Testing destination connection... ")
	dstDB, err := openDB(dstCfg)
	if err != nil {
		fmt.Printf("FAILED\n  %v\n", err)
		return
	}
	defer dstDB.Close()
	fmt.Println("OK")

	if isNewDst {
		dstCfg.Name = offerSave(dstCfg)
	}

	dstCfg.Database, err = pickDatabase(dstDB)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	dstTable, err := pickTable(dstDB, "Select destination table:")
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	fmt.Println()

	// --- Confirm & copy ---
	var confirm bool
	err = survey.AskOne(&survey.Confirm{
		Message: fmt.Sprintf("Copy %s  →  %s ?",
			fmt.Sprintf(srcCfg.label(), srcTable),
			fmt.Sprintf(dstCfg.label(), dstTable)),
	}, &confirm)
	if err != nil || !confirm {
		fmt.Println("Cancelled.")
		return
	}

	fmt.Println("Copying...")
	if err := copyTable(srcDB, srcTable, dstDB, dstTable); err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Println("Done!")
}
