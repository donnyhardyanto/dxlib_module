package oam

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/api/oam"
	"github.com/donnyhardyanto/dxlib/utils/http"
)

func DefineAPIEndPoints(anAPI *api.DXAPI) (err error) {
	anAPI.NewEndPoint("PING",
		"Receive Ping and send out Pong. Used to indicate the service is active and well.",
		"/ping", "GET", api.EndPointTypeHTTPJSON, http.ContentTypeApplicationJSON, nil,
		oam.Ping, nil, nil, nil, nil,
	)

	anAPI.NewEndPoint("PrintSpec",
		"Print the API Specification",
		"/spec", "GET", api.EndPointTypeHTTPJSON, http.ContentTypeApplicationJSON, nil,
		anAPI.APIHandlerPrintSpec, nil, nil, nil, nil,
	)

	return nil
}
