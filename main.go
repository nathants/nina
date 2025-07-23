// main provides CLI entry point for nina with subcommand routing
// registers commands via init() in cmd packages and invokes them
package main


import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/alexflint/go-arg"
	_ "github.com/nathants/nina/cmd/arch"
	_ "github.com/nathants/nina/cmd/ask"
	_ "github.com/nathants/nina/cmd/auth"
	_ "github.com/nathants/nina/cmd/choose"
	_ "github.com/nathants/nina/cmd/edit"
	_ "github.com/nathants/nina/cmd/run"
	_ "github.com/nathants/nina/cmd/tools"
	"github.com/nathants/nina/lib"
)

func usage() {
	var fns []string
	maxLen := 0
	for fn := range lib.Commands {
		fns = append(fns, fn)
		if len(fn) > maxLen {
			maxLen = len(fn)
		}
	}
	sort.Strings(fns)
	fmtStr := "%-" + fmt.Sprint(maxLen) + "s %s\n"
	for _, fn := range fns {
		args := lib.Args[fn]
		val := reflect.ValueOf(args)
		newVal := reflect.New(val.Type())
		newVal.Elem().Set(val)
		p, err := arg.NewParser(arg.Config{}, newVal.Interface())
		if err != nil {
			fmt.Println("Error creating parser:", err)
			return
		}
		var buffer bytes.Buffer
		p.WriteHelp(&buffer)
		descr := buffer.String()
		lines := strings.Split(descr, "\n")
		var line string
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "Usage:") {
				line = l
			}
		}
		line = strings.ReplaceAll(line, "Usage: nina", "")
		fmt.Printf(fmtStr, fn, line)
	}
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage()
		os.Exit(1)
	}
	cmd := os.Args[1]
	fn, ok := lib.Commands[cmd]
	if !ok {
		usage()
		fmt.Fprintln(os.Stderr, "\nunknown command:", cmd)
		os.Exit(1)
	}
	os.Args = os.Args[1:]
	fn()
}
