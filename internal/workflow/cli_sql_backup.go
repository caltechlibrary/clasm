package workflow

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

const generateSQLBackupUsage = "usage: " + GenerateSQLBackupCLISlug + " <instance> <directory>"

// ParseSQLBackupArgs parses generate-sql-backup's two positional arguments. The
// instance matches the Name tag first, then the instance ID, and is an error
// rather than a guess if either matches more than one. Every problem is a
// *UsageError (exit 2) raised before AWS is touched; --help is a *HelpRequested.
// The form has no other options.
func ParseSQLBackupArgs(args []string, instances []inventory.Instance) (inventory.Instance, SQLBackupParams, error) {
	const leaf = GenerateSQLBackupCLISlug
	fs := flag.NewFlagSet(leaf, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the errors below carry the usage; nothing prints twice
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return inventory.Instance{}, SQLBackupParams{}, &HelpRequested{Usage: generateSQLBackupUsage + generateSQLBackupHelp}
		}
		return inventory.Instance{}, SQLBackupParams{}, &UsageError{Msg: fmt.Sprintf("%s: %v\n%s", leaf, err, generateSQLBackupUsage)}
	}
	pos := fs.Args()
	if len(pos) != 2 {
		return inventory.Instance{}, SQLBackupParams{}, &UsageError{Msg: fmt.Sprintf("%s: want 2 arguments, got %d\n%s", leaf, len(pos), generateSQLBackupUsage)}
	}
	inst, err := resolveInstanceArg(pos[0], instances)
	if err != nil {
		return inventory.Instance{}, SQLBackupParams{}, &UsageError{Msg: fmt.Sprintf("%s: %v", leaf, err)}
	}
	if pos[1] == "" {
		return inventory.Instance{}, SQLBackupParams{}, &UsageError{Msg: fmt.Sprintf("%s: directory must not be empty\n%s", leaf, generateSQLBackupUsage)}
	}
	return inst, SQLBackupParams{InstanceID: inst.InstanceID, Directory: pos[1]}, nil
}

const generateSQLBackupHelp = `

Writes a gzipped pg_dump of the instance's RDM database into <directory> on the
instance, named <container>-<database>-<date>.sql.gz, and makes the directory
and its dumps belong to the service user. It runs without a confirmation
prompt. A second run on the same day replaces that day's dump, exactly as the
instance's own backup script does.

Options:
  -h, --help
      Show this help.
`
