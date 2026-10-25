package storage

import (
	"fmt"
	"regexp"
	"strings"
)

// Remove literals/comments before inspecting statement verbs. Trigger bodies
// have their own BEGIN/END; transaction control outside them is never allowed.
var sqlLiterals = regexp.MustCompile(`(?s)'(?:''|[^'])*'|"(?:""|[^"])*"|\x60[^\x60]*\x60|\[[^\]]*\]|/\*.*?\*/|--[^\n]*`)

func validateMigrationSQL(query string) error {
	clean := sqlLiterals.ReplaceAllString(query, " ")
	trigger := false
	for _, statement := range strings.Split(clean, ";") {
		fields := strings.Fields(strings.ToUpper(statement))
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "CREATE" && (len(fields) > 1 && fields[1] == "TRIGGER" || len(fields) > 2 && (fields[1] == "TEMP" || fields[1] == "TEMPORARY") && fields[2] == "TRIGGER") {
			trigger = true
		}
		switch fields[0] {
		case "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "ATTACH", "DETACH", "VACUUM", "PRAGMA":
			return fmt.Errorf("migration must be transactional and database-local: forbidden %s", fields[0])
		case "END":
			if !trigger {
				return fmt.Errorf("migration cannot end its transaction")
			}
			trigger = false
		}
	}
	if regexp.MustCompile(`(?i)\b(load_extension|writefile|readfile)\s*\(`).MatchString(clean) {
		return fmt.Errorf("migration cannot invoke external effects")
	}
	return nil
}
