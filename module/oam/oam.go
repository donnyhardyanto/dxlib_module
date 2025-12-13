package oam

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/api/oam"
	"github.com/donnyhardyanto/dxlib/utils/http"
)

func DefineAPIEndPoints(anAPI *api.DXAPI) (err error) {
	anAPI.NewEndPoint("PING",
		"Receive Ping and send out Pong. Used to indicate the service is active and well.",
		"/ping", "GET", api.EndPointTypeHTTPJSON, http.RequestContentTypeApplicationJSON, nil,
		oam.Ping, nil, nil, nil, nil, 0, "",
	)

	anAPI.NewEndPoint("PrintSpec",
		"Print the API Specification",
		"/spec", "GET", api.EndPointTypeHTTPJSON, http.RequestContentTypeApplicationJSON, nil,
		anAPI.APIHandlerPrintSpec, nil, nil, nil, nil, 0, "",
	)

	return nil
}
