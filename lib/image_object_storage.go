package lib

import (
	"bytes"
	"fmt"
	"github.com/donnyhardyanto/dxlib/api"
	"github.com/donnyhardyanto/dxlib/object_storage"
	"golang.org/x/image/draw"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
)

type ProcessedImageObjectStorage struct {
	ObjectStorageNameId string
	Width               int
	Height              int
}

type ImageObjectStorage struct {
	ObjectStorageSourceNameId string
	MaxRequestSize            int64
	ProcessedImages           map[string]ProcessedImageObjectStorage
}

const MaxRequestSize = 100 * 1024 * 1024 // 100MB

func checkImageFormat(buf *bytes.Buffer) (string, error) {
	_, format, err := image.DecodeConfig(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return "", fmt.Errorf("failed to decode image config: %v", err.Error())
	}

	if format == "" {
		return "", fmt.Errorf("unknown image format")
	}

	return format, nil
}

func NewImageObjectStorage(objectStorageSourceNameId string, processedImages map[string]ProcessedImageObjectStorage) *ImageObjectStorage {
	return &ImageObjectStorage{
		ObjectStorageSourceNameId: objectStorageSourceNameId,
		MaxRequestSize:            MaxRequestSize,
		ProcessedImages:           processedImages,
	}
}

func calculateAspectRatioHeight(originalWidth, originalHeight, targetWidth int) int {
	ratio := float64(originalHeight) / float64(originalWidth)
	return int(float64(targetWidth) * ratio)
}

func (ios *ImageObjectStorage) Update(aepr *api.DXAPIEndPointRequest, filename string) (err error) {

	// Check the request size
	if aepr.Request.ContentLength > ios.MaxRequestSize {
		return aepr.WriteResponseAndNewErrorf(http.StatusRequestEntityTooLarge, "", "REQUEST_ENTITY_TOO_LARGE")
	}

	objectStorage, exists := object_storage.Manager.ObjectStorages[ios.ObjectStorageSourceNameId]
	if !exists {
		return aepr.WriteResponseAndNewErrorf(http.StatusNotFound, "", `OBJECT_STORAGE_NAME_NOT_FOUND:%s`, ios.ObjectStorageSourceNameId)
	}

	bodyLen := aepr.Request.ContentLength
	aepr.Log.Infof("Request body length: %d", bodyLen)

	// Get the request body stream
	bs := aepr.Request.Body
	if bs == nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "", `FAILED_TO_GET_BODY_STREAM:%s`, ios.ObjectStorageSourceNameId)
	}
	// RequestRead the entire request body into a buffer
	var buf bytes.Buffer
	_, err = io.Copy(&buf, bs)
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "", `FAILED_TO_READ_REQUEST_BODY:%s=%v`, ios.ObjectStorageSourceNameId, err.Error())
	}

	// Upload the original file
	uploadInfo, err := objectStorage.UploadStream(bytes.NewReader(buf.Bytes()), filename, filename, "application/octet-stream", false, bodyLen)
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "", `FAILED_TO_UPLOAD_SOURCE_IMAGE_TO_OBJECT_STORAGE:%s=%v`, ios.ObjectStorageSourceNameId, err.Error())
	}

	aepr.Log.Infof("Original upload info result: %d", uploadInfo.Size)

	// Decode the image
	img, formatName, err := image.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, "", `FAILED_TO_DECODE_IMAGE:%s=%v`, ios.ObjectStorageSourceNameId, err.Error())
	}

	aepr.Log.Infof("Image format (using Image.Decode): %s", formatName)

	/*format, err := checkImageFormat(&buf)
	if err != nil {
		return aepr.WriteResponseAndNewErrorf(http.StatusUnprocessableEntity, `FAILED_TO_CHECK_IMAGE_FORMAT:%s=%v`, ios.ObjectStorageSourceNameId, err.Error())
	}

	aepr.Log.Infof("Image format (using Image.DecodeConfig): %s", format)
	*/
	bounds := img.Bounds()
	originalWidth := bounds.Dx()
	originalHeight := bounds.Dy()

	for _, processedImage := range ios.ProcessedImages {
		objectStorage, ok := object_storage.Manager.ObjectStorages[processedImage.ObjectStorageNameId]
		if !ok {
			return aepr.WriteResponseAndNewErrorf(http.StatusNotFound, "", `OBJECT_STORAGE_NAME_NOT_FOUND:%s`, processedImage.ObjectStorageNameId)
		}
		targetHeight := calculateAspectRatioHeight(originalWidth, originalHeight, processedImage.Width)
		resizedImg := image.NewRGBA(image.Rect(0, 0, processedImage.Width, targetHeight))

		// Resize the image
		draw.CatmullRom.Scale(resizedImg, resizedImg.Bounds(), img, img.Bounds(), draw.Over, nil)

		// Encode the resized image
		var resizedBuf bytes.Buffer

		err = png.Encode(&resizedBuf, resizedImg)
		if err != nil {
			return fmt.Errorf("RESIZED_IMAGE_PNG_ENCODE_FAILED:(%dx%d) %v", processedImage.Width, processedImage.Height, err.Error())
		}

		// Upload the resized image
		buf := resizedBuf.Bytes()
		bufLen := int64(len(buf))
		uploadInfo, err := objectStorage.UploadStream(bytes.NewReader(buf), filename, filename, "image/"+formatName, false, bufLen)
		if err != nil {
			return fmt.Errorf("FAILED_TO_UPLOAD_RESIZED_IMAGE_TO_OBJECT_STORAGE:(%s)=%v", processedImage.ObjectStorageNameId, err.Error())
		}

		aepr.Log.Infof("Resized (%dx%d) upload info result size: %d", processedImage.Width, processedImage.Height, uploadInfo.Size)
	}

	return nil
}

func (ios *ImageObjectStorage) DownloadSource(aepr *api.DXAPIEndPointRequest, filename string) (err error) {
	err = object_storage.Manager.FindObjectStorageAndSendObject(aepr, ios.ObjectStorageSourceNameId, filename)
	if err != nil {
		return err
	}

	return nil
}

func (ios *ImageObjectStorage) DownloadProcessedImage(aepr *api.DXAPIEndPointRequest, processedImageNameId string, filename string) (err error) {

	err = object_storage.Manager.FindObjectStorageAndSendObject(aepr, ios.ProcessedImages[processedImageNameId].ObjectStorageNameId, filename)
	if err != nil {
		return err
	}

	return nil
}
