package general

import (
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib/errors"
)

func (g *DxmGeneral) AnnouncementCreate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, title, err := aepr.GetParameterValueAsString("title")
	if err != nil {
		return err
	}
	_, content, err := aepr.GetParameterValueAsString("content")
	if err != nil {
		return err
	}
	_, err = g.Announcement.DoCreate(aepr, map[string]any{
		"title":   title,
		"content": content,
	})
	return err
}

func (g *DxmGeneral) AnnouncementPictureUpdate(aepr *api.DXAPIEndPointRequest) (err error) {
	_, id, err := aepr.GetParameterValueAsInt64("id")
	if err != nil {
		return err
	}

	_, _, err = g.Announcement.ShouldGetById(aepr.Context, &aepr.Log, id)
	if err != nil {
		return err
	}

	idAsString := utils.Int64ToString(id)

	filename := idAsString + ".webp"

	err = g.AnnouncementPicture.Update(aepr, filename, "")
	if err != nil {
		return err
	}
	return nil
}

func (g *DxmGeneral) AnnouncementPictureUpdateFileContentBase64(aepr *api.DXAPIEndPointRequest) (err error) {
	_, id, err := aepr.GetParameterValueAsInt64("id")
	if err != nil {
		return err
	}

	_, _, err = g.Announcement.ShouldGetById(aepr.Context, &aepr.Log, id)
	if err != nil {
		return err
	}

	idAsString := utils.Int64ToString(id)

	filename := idAsString + ".webp"

	_, fileContentBase64, err := aepr.GetParameterValueAsString("content_base64")
	if err != nil {
		return err
	}

	err = g.AnnouncementPicture.Update(aepr, filename, fileContentBase64)
	if err != nil {
		return err
	}
	return nil
}

func (g *DxmGeneral) AnnouncementPictureUpdateFileContentBase64ByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	_, uid, err := aepr.GetParameterValueAsString("uid")
	if err != nil {
		return err
	}

	_, announcement, err := g.Announcement.ShouldGetByUid(aepr.Context, &aepr.Log, uid)
	if err != nil {
		return err
	}

	announcementId, ok := announcement["id"].(int64)
	if !ok {
		return errors.Errorf("IMPOSSIBLE:ANNOUNCEMENT_ID_NOT_FOUND_IN_ANNOUNCEMENT")
	}

	idAsString := utils.Int64ToString(announcementId)

	filename := idAsString + ".webp"

	_, fileContentBase64, err := aepr.GetParameterValueAsString("content_base64")
	if err != nil {
		return err
	}

	err = g.AnnouncementPicture.Update(aepr, filename, fileContentBase64)
	if err != nil {
		return err
	}
	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadSource(aepr *api.DXAPIEndPointRequest) (err error) {
	_, id, err := aepr.GetParameterValueAsInt64("id")
	if err != nil {
		return err
	}

	_, _, err = g.Announcement.ShouldGetById(aepr.Context, &aepr.Log, id)
	if err != nil {
		return err
	}

	idAsString := utils.Int64ToString(id)

	filename := idAsString + ".webp"

	err = g.AnnouncementPicture.DownloadSource(aepr, filename)
	if err != nil {
		return err
	}

	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadSourceByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	_, uid, err := aepr.GetParameterValueAsString("uid")
	if err != nil {
		return err
	}

	_, announcement, err := g.Announcement.ShouldGetByUid(aepr.Context, &aepr.Log, uid)
	if err != nil {
		return err
	}

	announcementId, ok := announcement["id"].(int64)
	if !ok {
		return errors.Errorf("IMPOSSIBLE:ANNOUNCEMENT_ID_NOT_FOUND_IN_ANNOUNCEMENT")
	}

	idAsString := utils.Int64ToString(announcementId)

	filename := idAsString + ".webp"

	err = g.AnnouncementPicture.DownloadSource(aepr, filename)
	if err != nil {
		return err
	}

	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadByUidByProcessedNameId(aepr *api.DXAPIEndPointRequest, processedImageNameId string) (err error) {
	_, uid, err := aepr.GetParameterValueAsString("uid")
	if err != nil {
		return err
	}

	_, announcement, err := g.Announcement.ShouldGetByUid(aepr.Context, &aepr.Log, uid)
	if err != nil {
		return err
	}

	announcementId, ok := announcement["id"].(int64)
	if !ok {
		return errors.Errorf("IMPOSSIBLE:ANNOUNCEMENT_ID_NOT_FOUND_IN_ANNOUNCEMENT")
	}

	idAsString := utils.Int64ToString(announcementId)

	filename := idAsString + ".webp"
	err = g.AnnouncementPicture.DownloadProcessedImage(aepr, processedImageNameId, filename)
	if err != nil {
		return err
	}
	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadSmallByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByUidByProcessedNameId(aepr, "small")
}

func (g *DxmGeneral) AnnouncementPictureDownloadMediumByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByUidByProcessedNameId(aepr, "medium")
}

func (g *DxmGeneral) AnnouncementPictureDownloadBigByUid(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByUidByProcessedNameId(aepr, "big")
}

func (g *DxmGeneral) AnnouncementPictureDownloadByProcessedNameId(aepr *api.DXAPIEndPointRequest, processedImageNameId string) (err error) {
	_, id, err := aepr.GetParameterValueAsInt64("id")
	if err != nil {
		return err
	}

	_, _, err = g.Announcement.ShouldGetById(aepr.Context, &aepr.Log, id)
	if err != nil {
		return err
	}

	idAsString := utils.Int64ToString(id)

	filename := idAsString + ".webp"
	err = g.AnnouncementPicture.DownloadProcessedImage(aepr, processedImageNameId, filename)
	if err != nil {
		return err
	}
	return nil
}

func (g *DxmGeneral) AnnouncementPictureDownloadSmall(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByProcessedNameId(aepr, "small")
}

func (g *DxmGeneral) AnnouncementPictureDownloadMedium(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByProcessedNameId(aepr, "medium")
}

func (g *DxmGeneral) AnnouncementPictureDownloadBig(aepr *api.DXAPIEndPointRequest) (err error) {
	return g.AnnouncementPictureDownloadByProcessedNameId(aepr, "big")
}
