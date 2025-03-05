package general

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/pkg/errors"
)

func (g *DxmGeneral) AnnouncementList(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.Announcement.RequestPagingList(aepr)
}

func (g *DxmGeneral) AnnouncementListAll(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.Announcement.RequestPagingListAll(aepr)
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

func (g *DxmGeneral) AnnouncementPictureDownloadSourceByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	_, uid, err := aepr.GetParameterValueAsString(`uid`)
	if err != nil {
		return err
	}

	_, announcement, err := g.Announcement.ShouldGetByUid(&aepr.Log, uid)
	if err != nil {
		return err
	}

	announcementId, ok := announcement[`id`].(int64)
	if !ok {
		return errors.Errorf(`IMPOSSIBLE:ANNOUNCEMENT_ID_NOT_FOUND_IN_ANNOUNCEMENT`)
	}

	idAsString := utils.Int64ToString(announcementId)

	filename := idAsString + ".png"

	err = g.AnnouncementPicture.DownloadSource(aepr, filename)
	if err != nil {
		return err
	}

	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadByUidByProcessedNameId(aepr *api.DXAPIEndPointRequest, processedImageNameId string) (err error) {
	_, uid, err := aepr.GetParameterValueAsString(`uid`)
	if err != nil {
		return err
	}

	_, announcement, err := g.Announcement.ShouldGetByUid(&aepr.Log, uid)
	if err != nil {
		return err
	}

	announcementId, ok := announcement[`id`].(int64)
	if !ok {
		return errors.Errorf(`IMPOSSIBLE:ANNOUNCEMENT_ID_NOT_FOUND_IN_ANNOUNCEMENT`)
	}

	idAsString := utils.Int64ToString(announcementId)

	filename := idAsString + ".png"
	err = g.AnnouncementPicture.DownloadProcessedImage(aepr, processedImageNameId, filename)
	if err != nil {
		return err
	}
	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadSmallByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByUidByProcessedNameId(aepr, `small`)
}

func (g *DxmGeneral) AnnouncementPictureDownloadMediumByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByUidByProcessedNameId(aepr, `medium`)
}

func (g *DxmGeneral) AnnouncementPictureDownloadBigByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByUidByProcessedNameId(aepr, `big`)
}

func (g *DxmGeneral) AnnouncementPictureDownloadByProcessedNameId(aepr *api.DXAPIEndPointRequest, processedImageNameId string) (err error) {
	id := aepr.ParameterValues[`id`].Value.(int64)

	_, _, err = g.Announcement.ShouldGetById(&aepr.Log, id)
	if err != nil {
		return err
	}

	idAsString := utils.Int64ToString(id)

	filename := idAsString + ".png"
	err = g.AnnouncementPicture.DownloadProcessedImage(aepr, processedImageNameId, filename)
	if err != nil {
		return err
	}
	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadSmall(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByProcessedNameId(aepr, `small`)
}

func (g *DxmGeneral) AnnouncementPictureDownloadMedium(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByProcessedNameId(aepr, `medium`)
}

func (g *DxmGeneral) AnnouncementPictureDownloadBig(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByProcessedNameId(aepr, `big`)
}
