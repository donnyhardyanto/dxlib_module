package credentials_test

import (
	"github.com/donnyhardyanto/dxlib_module/v2/module/credentials"
	"github.com/donnyhardyanto/dxlib_module/v2/module/passkey"
	"github.com/donnyhardyanto/dxlib_module/v2/module/password"
	"github.com/donnyhardyanto/dxlib_module/v2/module/user_photo"
)

// PostgresConnection drops in wherever a DBStore takes a database.
var (
	_ password.Database   = (*credentials.PostgresConnection)(nil)
	_ passkey.Database    = (*credentials.PostgresConnection)(nil)
	_ user_photo.Database = (*credentials.PostgresConnection)(nil)
)
