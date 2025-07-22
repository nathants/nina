// auth provides main command handler for authentication subcommands
// routes to login, logout, and list subcommands based on arguments
// displays help when no subcommand provided or -h flag used
package auth

import (
	"fmt"
	"os"
	"github.com/alexflint/go-arg"
	"nina/lib"
)

func init() {
	lib.Commands["auth"] = authMain
	lib.Args["auth"] = authMainArgs{}
}

type authMainArgs struct {
	Subcommand string   `arg:"positional" help:"Subcommand to run (login, logout, list)"`
	Args       []string `arg:"positional" help:"Arguments for subcommand"`
}

func (authMainArgs) Description() string {
	return `auth - Manage authentication for AI providers

Available subcommands:
  login   - Authenticate with AI providers using OAuth
  logout  - Remove stored authentication credentials
  list    - List current authentication status`
}

func authMain() {
	// Parse just the subcommand first
	var args authMainArgs
	p, err := arg.NewParser(arg.Config{
		Program: "nina auth",
	}, &args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	
	// Check for help or no args
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		p.WriteHelp(os.Stdout)
		os.Exit(0)
	}
	
	// Parse to get subcommand
	err = p.Parse(os.Args[1:2])
	if err != nil {
		p.WriteHelp(os.Stderr)
		os.Exit(1)
	}
	
	// Set up args for subcommand
	if len(os.Args) > 2 {
		os.Args = append([]string{"nina auth " + args.Subcommand}, os.Args[2:]...)
	} else {
		os.Args = []string{"nina auth " + args.Subcommand}
	}
	
	// Route to appropriate subcommand
	switch args.Subcommand {
	case "login":
		login()
	case "logout":
		authLogout()
	case "list":
		authList()
	case "-h", "--help", "":
		p.WriteHelp(os.Stdout)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n", args.Subcommand)
		p.WriteHelp(os.Stderr)
		os.Exit(1)
	}
}
