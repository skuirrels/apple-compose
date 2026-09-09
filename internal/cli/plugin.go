package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/engine"
)

const pluginConfig = `abstract = "Define and run multi-container applications (apple-compose)"
author = "apple-compose"
`

func (a *App) pluginCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Install apple-compose as the `container compose` plugin",
	}
	var name string
	install := &cobra.Command{
		Use:   "install",
		Short: "Copy this binary into the container CLI's plugin directory so `container compose` works",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := pluginDir(name)
			if err != nil {
				return err
			}
			self, err := os.Executable()
			if err != nil {
				return err
			}
			self, _ = filepath.EvalSymlinks(self)
			bin := filepath.Join(dir, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				if os.IsPermission(err) {
					return fmt.Errorf("%w\nThe runtime is installed system-wide; run `sudo %s plugin install`", err, filepath.Base(os.Args[0]))
				}
				return err
			}
			if err := copyFile(self, filepath.Join(bin, name)); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(pluginConfig), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "Installed plugin %q at %s\nRun `container %s up` to use it.\n", name, dir, name)
			return nil
		},
	}
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the plugin",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := pluginDir(name)
			if err != nil {
				return err
			}
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "Removed %s\n", dir)
			return nil
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show where the plugin would be installed and whether it is",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := pluginDir(name)
			if err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(dir, "bin", name)); err == nil {
				fmt.Fprintf(os.Stdout, "installed: %s\n", dir)
			} else {
				fmt.Fprintf(os.Stdout, "not installed (would install to %s)\n", dir)
			}
			return nil
		},
	}
	for _, c := range []*cobra.Command{install, uninstall, status} {
		c.Flags().StringVar(&name, "name", "compose", "Plugin (subcommand) name")
		cmd.AddCommand(c)
	}
	return cmd
}

// pluginDir resolves <install-root>/libexec/container-plugins/<name>, where
// the container CLI looks for user plugins.
func pluginDir(name string) (string, error) {
	bin, err := engine.Find()
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(bin)
	if err == nil {
		bin = real
	}
	installRoot := filepath.Dir(filepath.Dir(bin))
	return filepath.Join(installRoot, "libexec", "container-plugins", name), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
