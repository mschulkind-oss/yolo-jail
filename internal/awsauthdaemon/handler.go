package awsauthdaemon

import (
	"context"
	"errors"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// HandlerConfig is what the request handler answers from.
type HandlerConfig struct {
	Broker     awsauth.Broker
	ConfigPath string
}

// BuildHandler serves the action protocol the jail-side adapter speaks.
//
// # THE JSON THIS RETURNS *IS* THE CONTAINER-CREDENTIALS BODY
//
// On success it is the four keys — AccessKeyId, SecretAccessKey, Token, Expiration
// — and on failure it is Code and Message, which is the 4xx body the SDK surfaces on
// the error it raises. The adapter therefore forwards this map verbatim and decides
// only the status code, which is what keeps the `SessionToken` -> `Token` rename to
// one place in the tree.
//
// # The request never names the profile, the role or the policy
//
// There is deliberately no field for any of the three. They are user-config only
// (OQ-SSO4): the workspace config is jail-writable, so an agent that could name a
// profile could point this service at the `admin` one. A request carries an action
// and nothing that changes what is minted.
func BuildHandler(config HandlerConfig) hostservice.Handler {
	return func(session *hostservice.Session) {
		action := field(session, "action")
		if action == "" {
			action = "credentials"
		}
		switch action {
		case "ping":
			_ = session.JSON(map[string]any{"pong": true, "pid": int64(os.Getpid())})
		case "credentials":
			// Session.JailID is the HOST-ASSERTED caller from the connection
			// preamble, which is what makes the audit line worth writing.
			result, err := config.Broker.Fetch(context.Background(), session.JailID)
			if err != nil {
				replyError(session, err)
				return
			}
			_ = session.JSON(result.Credential.ContainerCredentials())
		case "status":
			// FINGERPRINT-ONLY, so this action is safe to answer from a jail: there
			// is no field here a credential body could travel in.
			view := config.Broker.Status()
			if form, err := awsauth.DetectForm(config.ConfigPath,
				config.Broker.Config.Profile); err == nil {
				view["config_form"] = string(form)
			}
			_ = session.JSON(view)
		default:
			session.Stderr("unknown action: " + action + "\n")
			session.Exit(2)
		}
	}
}

func field(session *hostservice.Session, name string) string {
	value, _ := session.Get(name)
	result, _ := value.(string)
	return result
}

// replyError emits the 4xx body and exits non-zero, so the adapter can tell a
// refusal from a credential without parsing either.
//
// The message reaches the agent's error text, which is exactly where OQ-SSO6 wants
// a lapsed session to be read: `aws sso login --profile X` is in it verbatim, and
// nothing here waits for anybody to run it.
func replyError(session *hostservice.Session, err error) {
	body := map[string]any{"Code": "ServiceError", "Message": err.Error()}
	var mintErr *awsauth.MintError
	if errors.As(err, &mintErr) {
		body = mintErr.ContainerError()
	}
	session.Stderr(str(body["Code"]) + ": " + str(body["Message"]) + "\n")
	_ = session.JSON(body)
	session.Exit(1)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
