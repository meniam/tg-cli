package cli

import (
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"os"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) authCode(cmd *cobra.Command, _ []string) error {
	var code string
	var err error
	if f, ok := a.deps.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		code, err = a.ask(cmd.Context(), false, "Login code: ")
		if err == nil && (len(code) == 0 || len(code) > 64) {
			err = fail(exitcode.Usage, "input is empty or too long")
		}
	} else {
		code, err = secretLine(a.deps.Stdin, 64)
	}
	if err != nil {
		return fail(exitcode.Usage, "cannot read login code")
	}
	path, _ := cmd.Flags().GetString("password-file")
	c, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	var res rpc.AuthResult
	if err = a.call(cmd.Context(), cmd, c, rpc.MethodAuthCode, rpc.AuthCodeParams{Code: code}, &res); err != nil {
		return err
	}
	if res.State == rpc.AuthPasswordRequired && path != "" {
		pw, e := secretFile(path, 1024)
		if e != nil {
			return e
		}
		res = rpc.AuthResult{}
		if e = a.call(cmd.Context(), cmd, c, rpc.MethodAuthPassword, rpc.AuthPasswordParams{Password: pw}, &res); e != nil {
			return e
		}
	}
	return a.success(cmd, res)
}
func (a *app) authPassword(cmd *cobra.Command, _ []string) error {
	path, _ := cmd.Flags().GetString("password-file")
	stdin, _ := cmd.Flags().GetBool("password-stdin")
	if path != "" && stdin {
		return fail(exitcode.Usage, "choose --password-file or --password-stdin")
	}
	var pw string
	var err error
	if path != "" {
		pw, err = secretFile(path, 1024)
	} else if stdin {
		pw, err = secretLine(a.deps.Stdin, 1024)
	} else if f, ok := a.deps.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		pw, err = a.ask(cmd.Context(), true, "Two-factor password: ")
	} else {
		return fail(exitcode.Usage, "use --password-file or --password-stdin")
	}
	if err != nil || pw == "" || len(pw) > 1024 {
		return fail(exitcode.Usage, "cannot read password")
	}
	c, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	var res rpc.AuthResult
	if err = a.call(cmd.Context(), cmd, c, rpc.MethodAuthPassword, rpc.AuthPasswordParams{Password: pw}, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}
