package user_management

import (
	"strings"
	"testing"
)

func TestPasswordHashCreateRejectsPasswordBcryptCannotHash(t *testing.T) {
	um := DxmUserManagement{CurrentPasswordHashMethod: 2}

	// bcrypt refuses input longer than 72 bytes
	hash, err := um.passwordHashCreate(strings.Repeat("a", 73))
	if err == nil {
		t.Fatalf("expected an error, got hash %q", hash)
	}
}

func TestPasswordHashCreateAndVerify(t *testing.T) {
	for _, method := range []byte{2, 3} {
		um := DxmUserManagement{CurrentPasswordHashMethod: method}
		hash, err := um.passwordHashCreate("correct horse")
		if err != nil {
			t.Fatalf("method %d: %v", method, err)
		}
		ok, err := um.passwordHashVerify("correct horse", hash)
		if err != nil || !ok {
			t.Fatalf("method %d: right password not accepted (ok=%v, err=%v)", method, ok, err)
		}
		ok, err = um.passwordHashVerify("wrong horse", hash)
		if err != nil || ok {
			t.Fatalf("method %d: wrong password accepted (ok=%v, err=%v)", method, ok, err)
		}
	}
}
