package general

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/utils"
)

func (g *DxmGeneral) AnnouncementList(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.Announcement.RequestPagingList(aepr)
}

func (g *DxmGeneral) AnnouncementCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = g.Announcement.DoCreate(aepr, map[string]any{
		`title`:   aepr.ParameterValues[`title`].Value.(string),
		`content`: aepr.ParameterValues[`content`].Value.(string),
	})
	return err
}

func (g *DxmGeneral) AnnouncementRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.Announcement.RequestRead(aepr)
}

func (g *DxmGeneral) AnnouncementEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.Announcement.RequestEdit(aepr)
}

func (g *DxmGeneral) AnnouncementDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.Announcement.RequestSoftDelete(aepr)
}

func (g *DxmGeneral) AnnouncementPictureUpdate(aepr *api.DXAPIEndPointRequest) (err error) {
	id := aepr.ParameterValues[`id`].Value.(int64)

	_, _, err = g.Announcement.ShouldGetById(&aepr.Log, id)
	if err != nil {
		return err
	}

	idAsString := utils.Int64ToString(id)

	filename := idAsString + ".png"

	err = g.AnnouncementPicture.Update(aepr, filename)
	if err != nil {
		return err
	}
	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadSource(aepr *api.DXAPIEndPointRequest) (err error) {
	id := aepr.ParameterValues[`id`].Value.(int64)

	_, _, err = g.Announcement.ShouldGetById(&aepr.Log, id)
	if err != nil {
		return err
	}

	idAsString := utils.Int64ToString(id)

	filename := idAsString + ".png"

	err = g.AnnouncementPicture.DownloadSource(aepr, filename)
	if err != nil {
		return err
	}

	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadSmall(aepr *api.DXAPIEndPointRequest) (err error) {
	id := aepr.ParameterValues[`id`].Value.(int64)

	_, _, err = g.Announcement.ShouldGetById(&aepr.Log, id)
	if err != nil {
		return err
	}

	idAsString := utils.Int64ToString(id)

	filename := idAsString + ".png"

	err = g.AnnouncementPicture.DownloadProcessedImage(aepr, `small`, filename)
	if err != nil {
		return err
	}
	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadMedium(aepr *api.DXAPIEndPointRequest) (err error) {
	id := aepr.ParameterValues[`id`].Value.(int64)

	_, _, err = g.Announcement.ShouldGetById(&aepr.Log, id)
	if err != nil {
		return err
	}

	idAsString := utils.Int64ToString(id)

	filename := idAsString + ".png"

	err = g.AnnouncementPicture.DownloadProcessedImage(aepr, `medium`, filename)
	if err != nil {
		return err
	}
	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadBig(aepr *api.DXAPIEndPointRequest) (err error) {
	id := aepr.ParameterValues[`id`].Value.(int64)

	_, _, err = g.Announcement.ShouldGetById(&aepr.Log, id)
	if err != nil {
		return err
	}

	idAsString := utils.Int64ToString(id)

	filename := idAsString + ".png"

	err = g.AnnouncementPicture.DownloadProcessedImage(aepr, `big`, filename)
	if err != nil {
		return err
	}
	return nil
}

/*func (t *DxmGeneral) AnnouncementListDownload(aepr *api.DXAPIEndPointRequest) (err error) {
	isExistFilterWhere, filterWhere, err := aepr.GetParameterValueAsString("filter_where")
	if err != nil {
		return err
	}
	if !isExistFilterWhere {
		filterWhere = ""
	}
	isExistFilterOrderBy, filterOrderBy, err := aepr.GetParameterValueAsString("filter_order_by")
	if err != nil {
		return err
	}
	if !isExistFilterOrderBy {
		filterOrderBy = ""
	}

	isExistFilterKeyValues, filterKeyValues, err := aepr.GetParameterValueAsJSON("filter_key_values")
	if err != nil {
		return err
	}
	if !isExistFilterKeyValues {
		filterKeyValues = nil
	}

	_, format, err := aepr.GetParameterValueAsString("format")
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusBadRequest, `FORMAT_PARAMETER_ERROR:%s`, err.Error())
	}

	format = strings.ToLower(format)

	isDeletedIncluded := false
	if !isDeletedIncluded {
		if filterWhere != "" {
			filterWhere = fmt.Sprintf("(%s) and ", filterWhere)
		}

		switch t.Announcement.Database.DatabaseType.String() {
		case "sqlserver":
			filterWhere = filterWhere + "(is_deleted=0)"
		case "postgres":
			filterWhere = filterWhere + "(is_deleted=false)"
		default:
			filterWhere = filterWhere + "(is_deleted=0)"
		}
	}

	if t.Announcement.Database == nil {
		t.Announcement.Database = database.Manager.Databases[t.DatabaseNameId]
	}

	if !t.Announcement.Database.Connected {
		err := t.Announcement.Database.Connect()
		if err != nil {
			aepr.Log.Errorf("error At reconnect db At table %s list (%s) ", t.NameId, err.Error())
			return err
		}
	}

	rowsInfo, list, err := db.NamedQueryList(t.Announcement.Database.Connection, "*", t.Announcement.ListViewNameId,
		filterWhere, "", filterOrderBy, filterKeyValues)

	if err != nil {
		return err
	}

	// Set export options
	opts := export.ExportOptions{
		Format:     export.ExportFormat(format),
		SheetName:  "Sheet1",
		DateFormat: "2006-01-02 15:04:05",
	}

	// Get file as stream
	data, contentType, err := export.ExportToStream(rowsInfo, list, opts)
	if err != nil {
		return err
	}

	// Set response headers
	filename := fmt.Sprintf("export_%s_%s.%s", t.NameId, time.Now().Format("20060102_150405"), format)

	responseWriter := *aepr.GetResponseWriter()
	responseWriter.Header().Set("Content-Type", contentType)
	responseWriter.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	responseWriter.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	responseWriter.WriteHeader(http.StatusOK)
	aepr.ResponseStatusCode = http.StatusOK

	_, err = responseWriter.Write(data)
	if err != nil {
		return err
	}

	aepr.ResponseHeaderSent = true
	aepr.ResponseBodySent = true

	return nil
}
*/
