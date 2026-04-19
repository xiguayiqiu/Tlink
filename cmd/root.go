package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "tlink",
	Short: "Tlink - 简易文件传输工具",
	Long: `Tlink 是一个以文件传输下载为主要功能的工具：
- 文件下载（多线程、断点续传）
- 局域网文件传输`,
	Version: "版本: tlink-v0.1",
}

func init() {
	cobra.AddTemplateFunc("rpad", func(s string, padding int) string {
		if len(s) >= padding {
			return s
		}
		return s + strings.Repeat(" ", padding-len(s))
	})
	cobra.AddTemplateFunc("joinAliases", func(aliases []string) string {
		if len(aliases) == 0 {
			return ""
		}
		return "(简写: " + strings.Join(aliases, ", ") + ")"
	})
	cobra.AddTemplateFunc("indent", func(spaces int, s string) string {
		if s == "" {
			return ""
		}
		lines := strings.Split(s, "\n")
		var result []string
		for _, line := range lines {
			if line != "" {
				result = append(result, strings.Repeat(" ", spaces)+line)
			}
		}
		return strings.Join(result, "\n")
	})
	rootCmd.SetHelpTemplate(helpTemplate)
	rootCmd.SetVersionTemplate(`{{.Version}}
作者：弈秋忘忧白帽
`)
}

var helpTemplate = `{{.Short}}

{{.Long}}

Usage:
  {{.UseLine}}

Available Commands:
{{range .Commands}}{{if and (not .Hidden) (ne .Name "completion") (ne .Name "help")}}  {{rpad .Name 11}} {{rpad (joinAliases .Aliases) 15}} {{.Short}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces | indent 4}}
{{end}}{{end}}

Use "{{.CommandPath}} [command] --help" for more information about a command.
`

func Execute() {
	completionCmd, _, _ := rootCmd.Find([]string{"completion"})
	if completionCmd != nil {
		completionCmd.Hidden = true
	}
	helpCmd, _, _ := rootCmd.Find([]string{"help"})
	if helpCmd != nil {
		helpCmd.Hidden = true
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
