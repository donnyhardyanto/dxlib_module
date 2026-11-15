package credentials_test

import (
	"github.com/donnyhardyanto/dxlib_module/module/credentials"
	"github.com/donnyhardyanto/dxlib_module/module/passkey"
	"github.com/donnyhardyanto/dxlib_module/module/password"
	"github.com/donnyhardyanto/dxlib_module/module/user_photo"
)

// PostgresConnection drops in wherever a DBStore takes a database.
var (
	_ password.Database   = (*credentials.PostgresConnection)(nil)
	_ passkey.Database    = (*credentials.PostgresConnection)(nil)
	_ user_photo.Database = (*credentials.PostgresConnection)(nil)
)
