package general

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/utils"
)

func (g *DxmGeneral) AnnouncementList(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.Announcements.List(aepr)
}

func (g *DxmGeneral) AnnouncementCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, err = g.Announcements.DoCreate(aepr, map[string]any{
		`title`:   aepr.ParameterValues[`title`].Value.(string),
		`content`: aepr.ParameterValues[`content`].Value.(string),
	})
	return err
}

func (g *DxmGeneral) AnnouncementRead(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.Announcements.Read(aepr)
}

func (g *DxmGeneral) AnnouncementEdit(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.Announcements.Edit(aepr)
}

func (g *DxmGeneral) AnnouncementDelete(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.Announcements.SoftDelete(aepr)
}

func (g *DxmGeneral) AnnouncementPictureUpdate(aepr *api.DXAPIEndPointRequest) (err error) {
	id := aepr.ParameterValues[`id`].Value.(int64)

	_, _, err = g.Announcements.MustGetById(&aepr.Log, id)
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

	_, _, err = g.Announcements.MustGetById(&aepr.Log, id)
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

	_, _, err = g.Announcements.MustGetById(&aepr.Log, id)
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

func (g *DxmGeneral) AnnouncementPictureDownloadMiddle(aepr *api.DXAPIEndPointRequest) (err error) {
	id := aepr.ParameterValues[`id`].Value.(int64)

	_, _, err = g.Announcements.MustGetById(&aepr.Log, id)
	if err != nil {
		return err
	}

	idAsString := utils.Int64ToString(id)

	filename := idAsString + ".png"

	err = g.AnnouncementPicture.DownloadProcessedImage(aepr, `middle`, filename)
	if err != nil {
		return err
	}
	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadBig(aepr *api.DXAPIEndPointRequest) (err error) {
	id := aepr.ParameterValues[`id`].Value.(int64)

	_, _, err = g.Announcements.MustGetById(&aepr.Log, id)
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
