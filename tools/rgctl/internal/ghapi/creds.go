package ghapi

import (
	"errors"
)

// Mode is the credential shape a [Client] runs under.
type Mode int

// Credential modes.
const (
	// ModeApp uses the App's id and private key: the bot login is discovered
	// from GET /app and every org gets its own installation token.
	ModeApp Mode = iota + 1
	// ModeToken uses an operator token; the bot login must be given.
	ModeToken
)

func (m Mode) String() string {
	switch m {
	case ModeApp:
		return "app"
	case ModeToken:
		return "token"
	default:
		return "unknown"
	}
}

// Credentials are read from flags and the environment by the command layer.
// The private key is a path; its content is read once by [New] and never
// logged.
type Credentials struct {
	AppID          int64
	PrivateKeyFile string
	// Token is GH_TOKEN. It is never a flag.
	Token string
	// BotLogin is <slug>[bot]. Required under token auth; under App auth it
	// is discovered and this value is ignored.
	BotLogin string
	// Host is the GitHub Enterprise Server base URL or host name. Empty or
	// "github.com" means github.com.
	Host string
}

// Credential errors, each a usage error at the command layer.
var (
	ErrNoCredentials   = errors.New("no credentials: set --app-id and --private-key-file, or GH_TOKEN with --bot-login")
	ErrAppIDWithoutKey = errors.New("--app-id needs --private-key-file")
	ErrKeyWithoutAppID = errors.New("--private-key-file needs --app-id")
	ErrTokenNeedsLogin = errors.New("GH_TOKEN needs --bot-login (<app slug>[bot]) to know whose pull requests to match")
)

// Resolve picks the mode the credentials describe. App credentials win when
// both shapes are present; warning then says so.
func (c *Credentials) Resolve() (mode Mode, warning string, err error) {
	hasApp := c.AppID != 0 || c.PrivateKeyFile != ""
	switch {
	case c.AppID != 0 && c.PrivateKeyFile == "":
		return 0, "", ErrAppIDWithoutKey
	case c.AppID == 0 && c.PrivateKeyFile != "":
		return 0, "", ErrKeyWithoutAppID
	case hasApp && c.Token != "":
		return ModeApp, "both App credentials and GH_TOKEN are set; using the App", nil
	case hasApp:
		return ModeApp, "", nil
	case c.Token != "" && c.BotLogin == "":
		return 0, "", ErrTokenNeedsLogin
	case c.Token != "":
		return ModeToken, "", nil
	default:
		return 0, "", ErrNoCredentials
	}
}
