package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not find video metadata", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized video access", err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	multipartFile, fileHeader, err := r.FormFile("video")
	if err != nil {
		return
	}
	defer multipartFile.Close()
	mediaType, _, err := mime.ParseMediaType(fileHeader.Header.Get("Content-Type"))
	if err != nil || mediaType != "video/mp4" {
		return
	}
	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()
	_, err = io.Copy(tempFile, multipartFile)
	if err != nil {
		return
	}
	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		return
	}
	fileBytes := make([]byte, 32)
	_, err = rand.Read(fileBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error", err)
		return
	}

	ratio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error", err)
		return
	}
	fileS3Folder := ""
	switch ratio {
	case "16:9":
		fileS3Folder = "landscape"
	case "9:16":
		fileS3Folder = "portrait"
	default:
		fileS3Folder = "other"
	}
	fileNamePrefix := base64.RawURLEncoding.EncodeToString(fileBytes)
	extension := strings.SplitN(mediaType, "/", 2)[1]
	fileName := fmt.Sprintf("%s/%s.%s", fileS3Folder, fileNamePrefix, extension)
	fastStartVideo, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error", err)
		return
	}
	openedFastStartFile, err := os.Open(fastStartVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error", err)
		return
	}
	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileName,
		ContentType: &mediaType,
		Body:        openedFastStartFile,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error", err)
		return
	}
	
	videoUrl := fmt.Sprintf("https://%s/%s", cfg.s3CfDistribution, fileName)
	video.VideoURL = &videoUrl

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error", err)
		return
	}
	respondWithJSON(w, http.StatusOK, video)
}

func processVideoForFastStart(filePath string) (string, error) {
	newFilePath := filePath + ".processing"
	err := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", newFilePath).Run()
	if err != nil {
		return "", err
	}
	return newFilePath, nil
}

func getVideoAspectRatio(filePath string) (string, error) {
	command := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var commandBuffer bytes.Buffer
	command.Stdout = &commandBuffer
	err := command.Run()
	if err != nil {
		return "", err
	}
	probe := ProbeResult{}
	err = json.Unmarshal(commandBuffer.Bytes(), &probe)
	if err != nil {
		return "", err
	}
	var videoProbe Stream
	found := false
	for _, stream := range probe.Streams {
		if stream.CodecType == "video" {
			videoProbe = stream
			found = true
			break
		}
	}
	if !found {
		return "other", nil
	}
	ratio := calculateRatio(videoProbe.Width, videoProbe.Height)
	if ratio == "16:9" || ratio == "9:16" {
		return ratio, nil
	}
	return "other", nil
}

func calculateRatio(width, height int) string {
	portraitRatio := 9.0 / 16.0
	landscapeRatio := 16.0 / 9.0
	videoRatio := float64(width) / float64(height)
	if (math.Abs(portraitRatio-videoRatio) / ((portraitRatio + videoRatio) / 2) * 100) < 2 {
		return "9:16"
	}
	if (math.Abs(landscapeRatio-videoRatio) / ((landscapeRatio + videoRatio) / 2) * 100) < 2 {
		return "16:9"
	}
	return "other"
}

type ProbeResult struct {
	Streams []Stream `json:"streams"`
}

type Stream struct {
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	CodecType string `json:"codec_type"`
}
