package main

import (
	"net/http"
	"io"
	"os"
	"mime"
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/google/uuid"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 10 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

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

	videoDB, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get video data", err)
		return 
	}
	if videoDB.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You're not the video owner", err)
		return
	}


	video, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse the video", err)
		return
	}
	defer video.Close()

	videoType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", err)
		return
	}
	if videoType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid file type", nil)
		return
	}

	tmpVideo, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create the temp video on disk", nil)
		return
	}
	defer os.Remove(tmpVideo.Name())
	defer tmpVideo.Close()

	_, err = io.Copy(tmpVideo, video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't save video to disk", err)
		return
	}

	_, err = tmpVideo.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't read the video again", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(tmpVideo.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't determine the aspect ratio of the video", err)
		return
	}

	key := make([]byte, 32)
	rand.Read(key)
	keyString := getVideoPath(base64.URLEncoding.EncodeToString(key), videoType, aspectRatio)

	processedVideoPath, err := processVideoForFastStart(tmpVideo.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't process video", err)
		return
	}
	defer os.Remove(processedVideoPath)


	processedVideo, err := os.Open(processedVideoPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't open processed video", err)
		return
	}
	defer processedVideo.Close()

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket: aws.String(cfg.s3Bucket),
		Key:    aws.String(keyString),
		Body:   processedVideo,
		ContentType: aws.String(videoType),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't upload video to s3", err)
		return
	}

	videoURL := cfg.getObjectURL(keyString)
	videoDB.VideoURL = &videoURL
	err = cfg.db.UpdateVideo(videoDB)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video url", err)
		return
	}

	videoRes, err := cfg.dbVideoToSignedVideo(videoDB)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't git the presigned URL for this video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoRes)
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error){
	presignClient := s3.NewPresignClient(s3Client)
	req, err := presignClient.PresignGetObject(
		context.TODO(), 
		&s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key: aws.String(key),
		},
		s3.WithPresignExpires(expireTime),
	)
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, nil
	}
	params := strings.Split(*video.VideoURL, ",")
	if len(params) != 2 {
		return video, nil
	}
	presignedURL, err := generatePresignedURL(cfg.s3Client, params[0], params[1], 5 * time.Minute)
	if err != nil {
		return video, err
	}
	video.VideoURL = &presignedURL
	return video, nil
}