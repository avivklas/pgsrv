package pgsrv

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"fmt"
)

const expectedPasswordMessage = "expected password response, got message type %c"
const passwordDidNotMatch = "Password does not match for user \"%s\""

// authenticator interface defines objects able to perform user authentication
// that happens at the very beginning of every session.
type authenticator interface {
	// authenticate accepts a msgReadWriter instance and a map of args that describe
	// the current session. It returns true if the authentication succeeded,
	// and the client was notified using the provided msgReadWriter object. It returns
	// an error if something went wrong, even if the authentication was successful:
	// e.g if it couldn't write a response into rw.
	//
	// Authentication errors as well as welcome messages are sent by this function,
	// so there is no need for the caller to send these. It is caller's responsibility
	// though to terminate the session in case that the first return value is false
	// OR if the second returned value is not nil.
	authenticate(rw msgReadWriter, args map[string]interface{}) (bool, error)
}

// noPasswordAuthenticator responds with auth OK immediately.
type noPasswordAuthenticator struct{}

func (np *noPasswordAuthenticator) authenticate(rw msgReadWriter, args map[string]interface{}) (bool, error) {
	return true, rw.Write(authOKMsg())
}

// messageReadWriter describes objects that handle client-server communication.
// Objects implementing this interface are used to send password requests to users,
// and receive their responses.
type msgReadWriter interface {
	Write(m msg) error
	Read() (msg, error)
}

// passwordProvider describes objects that are able to provide a password given a user name.
type passwordProvider interface {
	getPassword(user string) ([]byte, error)
}

// constantPasswordProvider is a password provider that always returns the same password,
// which it is given during the initialization.
type constantPasswordProvider struct {
	password []byte
}

func (cpp *constantPasswordProvider) getPassword(user string) ([]byte, error) {
	return cpp.password, nil
}

// md5ConstantPasswordProvider is a password provider that returns md5 hash of a given
// username and a constant password as md5(concat(password, user)).
type md5ConstantPasswordProvider struct {
	password []byte
}

func (cpp *md5ConstantPasswordProvider) getPassword(user string) ([]byte, error) {
	pu := append(cpp.password, []byte(user)...)
	puHash := md5.Sum(pu)
	return puHash[:], nil
}

// clearTextAuthenticator requests and accepts a clear text password.
// It is not recommended to use it for security reasons.
//
// It requires a passwordProvider implementation to verify that the provided password is correct.
type clearTextAuthenticator struct {
	pp passwordProvider
}

func (a *clearTextAuthenticator) authenticate(rw msgReadWriter, args map[string]interface{}) (bool, error) {
	// AuthenticationClearText
	passwordRequest := msg{
		'R',
		0, 0, 0, 8, // length
		0, 0, 0, 3, // clear text auth type
	}

	err := rw.Write(passwordRequest)
	if err != nil {
		return false, err
	}

	m, err := rw.Read()
	if err != nil {
		return false, err
	}

	if m.Type() != 'p' {
		err = fmt.Errorf(expectedPasswordMessage, m.Type())
		m := errMsg(WithSeverity(fromErr(err), FATAL))
		return false, rw.Write(m)
	}

	user := args["user"].(string)
	expectedPassword, err := a.pp.getPassword(user)
	actualPassword := extractPassword(m)

	if !bytes.Equal(expectedPassword, actualPassword) {
		err = fmt.Errorf(passwordDidNotMatch, user)
		m := errMsg(WithSeverity(fromErr(err), FATAL))
		return false, rw.Write(m)
	}

	return true, rw.Write(authOKMsg())
}

// md5Authenticator requests and accepts an MD5 hashed password from the client.
//
// It requires a passwordProvider implementation to verify that the provided password is correct.
type md5Authenticator struct {
	pp passwordProvider
}

func (a *md5Authenticator) authenticate(rw msgReadWriter, args map[string]interface{}) (bool, error) {
	// AuthenticationMD5Password
	passwordRequest := msg{
		'R',
		0, 0, 0, 12, // length
		0, 0, 0, 5, // md5 auth type
	}
	salt := getRandomSalt()
	passwordRequest = append(passwordRequest, salt...)

	err := rw.Write(passwordRequest)
	if err != nil {
		return false, err
	}

	m, err := rw.Read()
	if err != nil {
		return false, err
	}

	if m.Type() != 'p' {
		err = fmt.Errorf(expectedPasswordMessage, m.Type())
		m := errMsg(WithSeverity(fromErr(err), FATAL))
		return false, rw.Write(m)
	}

	user := args["user"].(string)
	storedHash, err := a.pp.getPassword(user)
	expectedHash := hashWithSalt(storedHash, salt)

	actualHash := extractPassword(m)

	if !bytes.Equal(expectedHash, actualHash) {
		err = fmt.Errorf(passwordDidNotMatch, user)
		m := errMsg(WithSeverity(fromErr(err), FATAL))
		return false, rw.Write(m)
	}

	return true, rw.Write(authOKMsg())
}

// authOKMsg returns a message that indicates that the client is now authenticated.
func authOKMsg() msg {
	return []byte{'R', 0, 0, 0, 8, 0, 0, 0, 0}
}

// getRandomSalt returns a cryptographically secure random slice of 4 bytes.
func getRandomSalt() []byte {
	salt := make([]byte, 4)
	rand.Read(salt)
	return salt
}

// extractPassword extracts the password from a provided 'p' message.
// It assumes that the message is valid.
func extractPassword(m msg) []byte {
	// password starts after the size (4 bytes) and lasts until null-terminator
	return m[5 : len(m)-1]
}

// hashWithSalt salts the provided md5 hash and hashes the result using md5.
// The provided hash must be md5(concat(password, username))
func hashWithSalt(hash, salt []byte) []byte {
	// concat('md5', md5(concat(md5(concat(password, username)), random-salt)))
	// the inner part (md5(concat())) is provided as hash argument
	puHash := fmt.Sprintf("%x", hash)
	puHashSalted := append([]byte(puHash), salt...)
	finalHash := fmt.Sprintf("md5%x", md5.Sum(puHashSalted))
	return []byte(finalHash)
}
