package commands

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// alignEnvNames renames the environment commands to the names the product
// uses (docs/product/product-language.md, IA spec section 7): new, rm, set,
// unset, file put/rm and protect. The old spellings keep working: aliases where
// the glossary allows them, hidden deprecated commands where it retires them.
func alignEnvNames(env *cobra.Command) {
	find := func(name string) *cobra.Command {
		for _, c := range env.Commands() {
			if c.Name() == name {
				return c
			}
		}
		return nil
	}
	rename := func(old, use string, aliases ...string) {
		if c := find(old); c != nil {
			c.Use = use
			c.Aliases = aliases
		}
	}
	rename("create", "new <name>", "create")
	rename("delete", "rm <name>", "delete", "remove")
	// --yes is the confirmation flag everywhere else in the CLI.
	if rm := find("rm"); rm != nil {
		if f := rm.Flags().Lookup("force"); f != nil && rm.Flags().Lookup("yes") == nil {
			rm.Flags().AddFlag(&pflag.Flag{Name: "yes", Shorthand: "y", Usage: "Same as --force", Value: f.Value, DefValue: f.DefValue, NoOptDefVal: f.NoOptDefVal})
		}
	}
	rename("set-var", "set [NAME]", "set-var")
	rename("unset-var", "unset <KEY>...", "unset-var")

	// file put / file rm, with the old flat spellings kept but hidden.
	if put, rm := find("add-file"), find("rm-file"); put != nil && rm != nil {
		env.RemoveCommand(put, rm)
		file := &cobra.Command{Use: "file", Short: "Add and remove secret files", Long: "Secret files are written into the machine when it starts. They are stored encrypted and never shown again."}
		newPut, newRm := newEnvAddFileCmd(), newEnvRmFileCmd()
		newPut.Use = "put <relpath> <local-file>"
		newRm.Use = "rm <relpath>"
		file.AddCommand(newPut, newRm)
		env.AddCommand(file)
		for _, old := range []*cobra.Command{newEnvAddFileCmd(), newEnvRmFileCmd()} {
			old.Hidden = true
			old.Deprecated = "use 'miosa env file put' and 'miosa env file rm'"
			env.AddCommand(old)
		}
	}

	// Safe for third parties is "Protected".
	if old := find("safe-third-parties"); old != nil {
		env.RemoveCommand(old)
		protect := newEnvSafeCmd()
		protect.Use = "protect <on|off>"
		env.AddCommand(protect)
		legacy := newEnvSafeCmd()
		legacy.Hidden = true
		legacy.Deprecated = "use 'miosa env protect'"
		env.AddCommand(legacy)
	}
}
