package main

import (
	"os"
	"os/exec"
	"bytes"
	"encoding/json"

	"path/filepath"
	"path"
	"fmt"
	"strings"
)

func (cfg apiConfig) ensureAssetsDir() error {
	if _, err := os.Stat(cfg.assetsRoot); os.IsNotExist(err) {
		return os.Mkdir(cfg.assetsRoot, 0755)
	}
	return nil
}

func getAssetPath(keyString string, mediaType string) string {
	ext := mediaTypeToExt(mediaType)
	return fmt.Sprintf("%s%s", keyString, ext)
}

func getVideoPath(keyString string, mediaType string, aspectRatio string) string {
	prefix := "other"
	if aspectRatio == "16:9" {
		prefix = "landscape"
	} else if aspectRatio == "9:16" {
		prefix = "portrait"
	}
	return path.Join(prefix, getAssetPath(keyString, mediaType))
}

func (cfg apiConfig) getObjectURL(key string) string {
	return fmt.Sprintf("%s,%s", cfg.s3Bucket, key)
}

func (cfg apiConfig) getAssetDiskPath(assetPath string) string {
	return filepath.Join(cfg.assetsRoot, assetPath)
}

func (cfg apiConfig) getAssetURL(assetPath string) string {
	return fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, assetPath)
}

func mediaTypeToExt(mediaType string) string {
	parts := strings.Split(mediaType, "/")
	if len(parts) != 2 {
		return ".bin"
	}
	return "." + parts[1]
}


func getVideoAspectRatio(filePath string) (string, error) {
	type videoData struct {
		Streams []struct{
			Width int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}

	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var outputBuffer bytes.Buffer
	cmd.Stdout = &outputBuffer

	err := cmd.Run()
	if err != nil {
		return "", err
	}

	var output videoData
	if err := json.Unmarshal(outputBuffer.Bytes(), &output); err != nil{
		return "", err
	}

	if len(output.Streams) == 0 {
		return "", fmt.Errorf("no video stream found")
	}

	if output.Streams[0].Height == 0 {
		return "", fmt.Errorf("the video's height is zero")
	}

	ratio := float64(output.Streams[0].Width) / float64(output.Streams[0].Height)
	if ratio > 1.5 && ratio < 1.9 {
		return "16:9", nil
	} else if ratio > 0.4 && ratio < 0.6 {
		return "9:16", nil
	}
	return "other", nil 
}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := fmt.Sprintf("%s%s", filePath, ".processing")
	cmd := exec.Command(
		"ffmpeg",
		"-i",
		filePath,
		"-c",
		"copy",
		"-movflags",
		"faststart",
		"-f",
		"mp4",
		outputFilePath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr


	err := cmd.Run()
	if err != nil {
		return "", err
	}

	fileInfo, err := os.Stat(outputFilePath)
	if err != nil {
		return "", fmt.Errorf("could not stat processed file: %v", err)
	}
	if fileInfo.Size() == 0 {
		return "", fmt.Errorf("processed file is empty")
	}

	return outputFilePath, nil
}
