// edit converts search/replace into line edits via ConvertToRangeUpdates
// takes search file, replace file, and target file to perform replacements
// uses lib.ConvertToRangeUpdates for accurate line-based editing
package edit

import (
	"context"
	"fmt"
	"github.com/nathants/nina/lib"
	util "github.com/nathants/nina/util"
	"os"
	"strings"

	"github.com/alexflint/go-arg"
)

func init() {
	lib.Commands["edit"] = edit
	lib.Args["edit"] = editArgs{}
}

type editArgs struct {
	Search  string `arg:"positional,required" help:"file with search text"`
	Replace string `arg:"positional,required" help:"file with replacement text"`
	Target  string `arg:"positional,required" help:"file to edit"`
}

func (editArgs) Description() string {
	return `edit - Edit a file via search and replace

Search must be a single section of entire contiguous lines as text.
Replace will replace those lines in Target.`
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return []string{}, nil
	}
	return strings.Split(text, "\n"), nil
}

func run(args editArgs) error {
	ctx := context.Background()

	search, err := readLines(args.Search)
	if err != nil {
		return err
	}
	replace, err := readLines(args.Replace)
	if err != nil {
		return err
	}
	origBytes, err := os.ReadFile(args.Target)
	if err != nil {
		return err
	}
	orig := string(origBytes)

	update := util.FileUpdate{
		FileName:     args.Target,
		SearchLines:  search,
		ReplaceLines: replace,
	}

	session := &util.SessionState{
		OrigFiles:     map[string]string{args.Target: orig},
		SelectedFiles: map[string]string{},
		PathMap:       map[string]string{args.Target: args.Target},
	}

	updates, err := lib.ConvertToRangeUpdates(ctx, []util.FileUpdate{update}, session, nil)
	if err != nil {
		return err
	}

	newContent, err := util.ApplyFileUpdates(orig, updates)
	if err != nil {
		return err
	}

	return os.WriteFile(args.Target, []byte(newContent), 0644)
}

func edit() {
	var args editArgs
	arg.MustParse(&args)
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
