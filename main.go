// kk-branches/main.go
package main

import (
	"os"

	"github.com/localshred/kk-branches/cmd"
	"github.com/localshred/koolkit/pkg/plugin"
)

func pluginMeta() plugin.Meta {
	return plugin.Meta{
		Describe: "Interactive manager for local branches, their PRs, and worktrees",
		Requires: []string{"git", "gh"},
	}
}

func main() {
	if handled, code := plugin.Handle(os.Args[1:], pluginMeta(), os.Stdout); handled {
		os.Exit(code)
	}
	os.Exit(cmd.Execute())
}
