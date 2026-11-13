package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func (a *app) authCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Log in to a Telegram account"}
	login := a.annotate(&cobra.Command{
		Use:   "login",
		Short: "Log in by phone number, login code and two-factor password",
		Long: "Log in by phone number. The login code and the two-factor password are asked\n" +
			"interactively and are never accepted as flags, so they stay out of the shell\n" +
			"history. With --qr no code is needed: scan the QR code in the Telegram app of\n" +
			"a logged-in device (Settings, Devices, Link Desktop Device). The daemon must be\n" +
			"running: `tg-cli daemon start`.",
		Args: cobra.NoArgs,
		RunE: a.authLogin,
	}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.AuthResult]()})
	login.Flags().String("phone", "", "phone number in international format; asked when omitted")
	login.Flags().Bool("qr", false, "log in by scanning a QR code in the Telegram app of a logged-in device, no code is needed")
	cancel := a.annotate(&cobra.Command{
		Use:   "cancel",
		Short: "Cancel a login that is waiting for a code or a password",
		Args:  cobra.NoArgs,
		RunE:  a.authCancel,
	}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.AuthCancelResult]()})
	status := a.annotate(&cobra.Command{
		Use:   "status",
		Short: "Show whether the profile is logged in, and as whom",
		Args:  cobra.NoArgs,
		RunE:  a.authStatus,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[authStatus]()})
	resend := a.annotate(&cobra.Command{
		Use:   "resend",
		Short: "Ask Telegram to send the login code another way (for example by SMS)",
		Long: "Ask Telegram to send the login code another way. Run it from a second\n" +
			"terminal while `auth login` waits for the code; then type the new code in the\n" +
			"first one. Typing resend at the code prompt does the same.",
		Args: cobra.NoArgs,
		RunE: a.authResend,
	}, noRisk, commandSpec{Safety: safetyAccountWrite, Result: typeOf[rpc.AuthResult]()})
	cmd.AddCommand(login, status, resend, cancel)
	return cmd
}

func (a *app) authResend(cmd *cobra.Command, _ []string) error {
	c, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	var res rpc.AuthResult
	if err := a.call(cmd.Context(), cmd, c, rpc.MethodAuthResend, nil, &res); err != nil {
		return err
	}
	a.warn(cmd, "%s", codeHint(res))
	return a.success(cmd, res)
}

// authStatus is the account part of the daemon status.
type authStatus struct {
	Profile    string    `json:"profile"`
	Connected  bool      `json:"connected"`
	Authorized bool      `json:"authorized"`
	User       *rpc.User `json:"user,omitempty"`
	LoginState string    `json:"login_state,omitempty"`
}

func (a *app) authStatus(cmd *cobra.Command, _ []string) error {
	c, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	var st rpc.StatusResult
	if err := a.call(cmd.Context(), cmd, c, rpc.MethodStatus, nil, &st); err != nil {
		return err
	}
	return a.success(cmd, authStatus{
		Profile:    st.Profile,
		Connected:  st.Connected,
		Authorized: st.Authorized,
		User:       st.User,
		LoginState: st.LoginState,
	})
}

func (a *app) authCancel(cmd *cobra.Command, _ []string) error {
	c, err := a.connect(cmd.Context(), cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	var res rpc.AuthCancelResult
	if err := a.call(cmd.Context(), cmd, c, rpc.MethodAuthCancel, nil, &res); err != nil {
		return err
	}
	return a.success(cmd, res)
}

// ask reads one answer. The read runs in its own goroutine so that an
// interrupt (a canceled context) ends the wait: a blocked terminal read cannot
// be stopped otherwise. The goroutine ends with the process.
func (a *app) ask(ctx context.Context, secret bool, prompt string) (string, error) {
	type answer struct {
		value string
		err   error
	}
	read := a.deps.ReadLine
	if secret {
		read = a.deps.ReadSecret
	}
	done := make(chan answer, 1)
	go func() {
		v, err := read(prompt)
		done <- answer{v, err}
	}()
	select {
	case r := <-done:
		return r.value, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// codeHint tells the user where to look for the login code and, when
// Telegram offers one, how to ask for another delivery.
func codeHint(res rpc.AuthResult) string {
	var hint string
	switch res.Delivery {
	case rpc.DeliveryApp:
		hint = "The code was sent to the Telegram app on your other devices (chat \"Telegram\"), not by SMS."
	case rpc.DeliverySMS:
		hint = "The code was sent by SMS."
	case rpc.DeliveryCall:
		hint = "The code is delivered by a phone call."
	case rpc.DeliveryEmail:
		hint = "The code was sent to your e-mail."
	default:
		hint = "Telegram did not say where the code was sent: check the Telegram app and your SMS."
	}
	if res.NextDelivery == "" {
		return hint
	}
	way := "another way"
	switch res.NextDelivery {
	case rpc.DeliverySMS:
		way = "by SMS"
	case rpc.DeliveryCall:
		way = "by a phone call"
	}
	hint += " If it does not arrive, type resend to get it " + way
	if res.ResendAfterSeconds > 0 {
		hint += fmt.Sprintf(" (Telegram allows this after %d seconds)", res.ResendAfterSeconds)
	}
	return hint + "."
}

// codePrompt names the place the code comes from in the input prompt.
func codePrompt(delivery string) string {
	switch delivery {
	case rpc.DeliveryApp:
		return "Code (from Telegram app): "
	case rpc.DeliverySMS:
		return "Code (from SMS): "
	case rpc.DeliveryCall:
		return "Code (from phone call): "
	case rpc.DeliveryEmail:
		return "Code (from e-mail): "
	}
	return "Login code: "
}

func (a *app) authLogin(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	c, err := a.connect(ctx, cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	// Progress lines are hints: --quiet silences them. The prompts below are
	// not: the login cannot go on without an answer, so they always print.
	progress := func(format string, args ...any) { a.warn(cmd, format, args...) }
	quiet := a.settings(cmd).quiet

	byQR, _ := cmd.Flags().GetBool("qr")
	phone, _ := cmd.Flags().GetString("phone")
	if phone == "" && !byQR {
		if phone, err = a.ask(ctx, false, "Phone number (international format, e.g. +15551234567): "); err != nil {
			return a.readFailed(ctx, c, "the phone number", err)
		}
	}

	// Every step decodes into a fresh value: optional fields are omitted from
	// the JSON, so reusing one value would keep stale ones from the last step.
	step := func(method string, params any) (rpc.AuthResult, error) {
		var res rpc.AuthResult
		err := a.call(ctx, cmd, c, method, params, &res)
		return res, err
	}

	var res rpc.AuthResult
	if byQR {
		progress("Requesting a QR login token from Telegram...")
		res, err = step(rpc.MethodAuthStartQR, nil)
	} else {
		progress("Requesting the login code from Telegram...")
		res, err = step(rpc.MethodAuthStart, rpc.AuthStartParams{Phone: phone})
	}
	if err != nil {
		return err
	}
	shownQR := false
	for res.State != rpc.AuthAuthorized {
		switch res.State {
		case rpc.AuthQRScan:
			if shownQR {
				progress("The QR code was refreshed, scan the new one:")
			} else {
				progress("Open Telegram on your phone: Settings, Devices, Link Desktop Device, and scan this code (it refreshes by itself, Ctrl-C cancels):")
			}
			shownQR = true
			if !quiet {
				if err := renderQR(a.deps.Stderr, res.QRURL, a.qrColor()); err != nil {
					a.cancelLogin(c)
					return fail(exitcode.Failure, "%v", err)
				}
			}
			progress("%s", res.QRURL)
			if res, err = step(rpc.MethodAuthWait, nil); err != nil {
				return err
			}
		case rpc.AuthCodeRequired:
			progress("%s", codeHint(res))
			code, err := a.ask(ctx, false, codePrompt(res.Delivery))
			if err != nil {
				return a.readFailed(ctx, c, "the login code", err)
			}
			if strings.EqualFold(strings.TrimSpace(code), "resend") {
				progress("Asking Telegram to send the code again...")
				if res, err = step(rpc.MethodAuthResend, nil); err != nil {
					return err
				}
				continue
			}
			progress("Signing in...")
			if res, err = step(rpc.MethodAuthCode, rpc.AuthCodeParams{Code: code}); err != nil {
				return err
			}
		case rpc.AuthPasswordRequired:
			progress("The account has two-factor authentication.")
			password, err := a.ask(ctx, true, "Two-factor password: ")
			if err != nil {
				return a.readFailed(ctx, c, "the password", err)
			}
			progress("Checking the password...")
			if res, err = step(rpc.MethodAuthPassword, rpc.AuthPasswordParams{Password: password}); err != nil {
				return err
			}
		default:
			a.cancelLogin(c)
			return fail(exitcode.Failure, "unexpected login state %q", res.State)
		}
	}
	progress("Logged in%s.", accountLabel(res.User))
	return a.success(cmd, res)
}

// accountLabel formats " as First Last (@username)" for a progress line.
func accountLabel(u *rpc.User) string {
	if u == nil {
		return ""
	}
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	switch {
	case name != "" && u.Username != "":
		return fmt.Sprintf(" as %s (@%s)", name, u.Username)
	case name != "":
		return " as " + name
	case u.Username != "":
		return " as @" + u.Username
	}
	return ""
}

// readFailed ends a login whose input could not be read or was interrupted.
func (a *app) readFailed(ctx context.Context, c *rpc.Client, what string, err error) error {
	a.cancelLogin(c)
	if ctx.Err() != nil {
		return fail(exitcode.Failure, "interrupted, the login was canceled")
	}
	return fail(exitcode.Usage, "read %s: %v", what, err)
}

// cancelLogin stops a login the user abandoned. It uses a fresh context
// because the command context may be the thing that was canceled.
func (a *app) cancelLogin(c *rpc.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), a.deps.CallTimeout)
	defer cancel()
	_ = c.Call(ctx, rpc.MethodAuthCancel, nil, nil)
}

// qrColor reports whether the QR code may use ANSI colors.
func (a *app) qrColor() bool {
	return a.deps.Getenv("NO_COLOR") == ""
}
