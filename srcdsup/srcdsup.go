package srcdsup

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/leighmacdonald/srcdsup/config"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func update(ctx context.Context, log *zap.Logger, rules []*config.RulesConfig, remoteConfig []*config.RemoteConfig) error {
	for _, ruleSet := range rules {
		log.Debug("Updating", zap.String("rule", ruleSet.Name))

		matches, errGlob := filepath.Glob(path.Join(ruleSet.Root, ruleSet.Pattern))
		if errGlob != nil {
			log.Warn("Error globbing files", zap.Error(errGlob))

			continue
		}

		if matches == nil {
			continue
		}

		var fileCollection []fs.FileInfo

		for _, fileMatch := range matches {
			stat, errStat := os.Stat(fileMatch)
			if errStat != nil {
				log.Error("Could not read log file", zap.Error(errStat))

				continue
			}

			fileCollection = append(fileCollection, stat)
		}

		for _, file := range fileCollection {
			log.Info("New rule match found",
				zap.String("rule", ruleSet.Name),
				zap.String("name", file.Name()),
				zap.Int64("size", file.Size()),
				zap.Time("time", file.ModTime()))
		}

		if errUpload := upload(ctx, log, ruleSet, remoteConfig, fileCollection); errUpload != nil {
			return errors.Wrapf(errUpload, "Failed to upload new match")
		}
	}

	return nil
}

func upload(ctx context.Context, log *zap.Logger, rules *config.RulesConfig, remoteConfigs []*config.RemoteConfig, files []fs.FileInfo) error {
	for _, remoteConfig := range remoteConfigs {
		for _, remoteRule := range rules.Remotes {
			if remoteRule == remoteConfig.Name {
				if remoteConfig.Name == "" {
					return errors.Errorf("Failed to find remote: %v", remoteRule)
				}

				if handlerErr := uploadGbans(ctx, log, rules, remoteConfig, files); handlerErr != nil {
					log.Error("Upload handler error",
						zap.String("name", remoteConfig.Name),
						zap.Error(handlerErr))

					continue
				}
				log.Info("Upload completed successfully")

			}
		}
	}

	for _, filePath := range files {
		srcFile := rules.SrcFile(filePath)
		if errRemove := os.RemoveAll(srcFile); errRemove != nil {
			return errors.Wrapf(errRemove, "Could not cleanup source file")
		}

		log.Debug("Removing source file", zap.String("path", srcFile))
	}

	return nil
}

func compressReaderBytes(log *zap.Logger, demoName string, demoBytes []byte) (*bytes.Buffer, error) {
	var compressedDemo bytes.Buffer

	demoBufWriter := bufio.NewWriter(&compressedDemo)
	writer := zip.NewWriter(demoBufWriter)

	outFile, errWriter := writer.Create(demoName)
	if errWriter != nil {
		return nil, errors.Wrap(errWriter, "Failed to write body to zip")
	}

	if _, errWrite := outFile.Write(demoBytes); errWrite != nil {
		return nil, errors.Wrap(errWrite, "Failed to close writer")
	}

	if errClose := writer.Close(); errClose != nil {
		return nil, errors.Wrap(errClose, "Failed to close writer")
	}

	log.Debug("Compressed size", zap.Int("size", compressedDemo.Len()))

	return &compressedDemo, nil
}

func makeMultiPart(demo *bytes.Buffer, name string) ([]byte, string, error) {
	outBuffer := &bytes.Buffer{}
	multiPartWriter := multipart.NewWriter(outBuffer)

	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Type", "application/octet-stream")
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="demo"; filename="%s"`, name))

	postBody := &bytes.Buffer{}
	writer := multipart.NewWriter(postBody)

	fileWriter, errCreatePart := multiPartWriter.CreatePart(partHeader)
	if errCreatePart != nil {
		return nil, "", errors.Wrap(errCreatePart, "Failed to create part")
	}

	if _, errWrite := fileWriter.Write(demo.Bytes()); errWrite != nil {
		return nil, "", errors.Wrap(errWrite, "Failed to write demo part")
	}

	if err := writer.Close(); err != nil {
		return nil, "", errors.Wrap(errCreatePart, "Failed to close writer")
	}

	return outBuffer.Bytes(), writer.FormDataContentType(), nil
}

func compressDemo(log *zap.Logger, filePath string, name string) (*bytes.Buffer, error) {
	body, readErr := os.ReadFile(filePath)
	if readErr != nil {
		return nil, errors.Wrap(readErr, "Failed to read file")
	}

	compressedDemo, errCompress := compressReaderBytes(log, name, body)
	if errCompress != nil {
		return nil, errors.Wrap(errCompress, "Failed to compress file")
	}

	return compressedDemo, nil
}

func send(ctx context.Context, ruleSet *config.RulesConfig,
	curFile fs.FileInfo, remoteConfig *config.RemoteConfig,
) error {
	localCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	client := http.Client{Timeout: time.Second * 120}

	file, fileErr := os.Open(ruleSet.SrcFile(curFile))
	if fileErr != nil {
		return errors.Wrap(fileErr, "Failed to open demo")
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, errPart := writer.CreateFormFile("demo", filepath.Base(curFile.Name()))
	if errPart != nil {
		return errors.Wrap(fileErr, "Failed to create form file")
	}

	if _, err := io.Copy(part, file); err != nil {
		return errors.Wrap(fileErr, "Failed to copy form file")
	}

	if err := writer.Close(); err != nil {
		return errors.Wrap(fileErr, "Failed to close form file")
	}

	req, errReq := http.NewRequestWithContext(localCtx, http.MethodPost, remoteConfig.URL+"/api/demo", body)
	if errReq != nil {
		return errors.Wrapf(errReq, "Failed to create request")
	}

	req.Header.Set("Authorization", remoteConfig.AuthToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, errResp := client.Do(req)
	if errResp != nil {
		return errors.Wrapf(errResp, "Failed to upload entity")
	}

	_ = resp.Body.Close()

	return nil
}

func uploadGbans(ctx context.Context, log *zap.Logger, ruleSet *config.RulesConfig,
	remoteConfig *config.RemoteConfig, files []fs.FileInfo,
) error {
	for _, curFile := range files {
		log.Info("Uploading file",
			zap.String("remote", remoteConfig.Name),
			zap.String("file", curFile.Name()),
			zap.String("name", remoteConfig.Name))

		if errSend := send(ctx, ruleSet, curFile, remoteConfig); errSend != nil {
			log.Error("Failed to send file", zap.Error(errSend))

			continue
		}

		log.Info("Uploading successful",
			zap.String("remote", remoteConfig.Name),
			zap.String("file", curFile.Name()),
			zap.String("name", remoteConfig.Name))
	}

	return nil
}

func refreshTokens(ctx context.Context, log *zap.Logger) {
	type authRequest struct {
		Key string `json:"key"`
	}

	type authResponse struct {
		Token string `json:"token"`
	}

	fetchToken := func(remote *config.RemoteConfig) (string, error) {
		client := http.Client{Timeout: time.Second * 120}
		body, errEnc := json.Marshal(authRequest{
			Key: remote.Password,
		})

		if errEnc != nil {
			return "", errors.Wrap(errEnc, "Failed to encode json auth payload")
		}

		localCtx, cancel := context.WithTimeout(ctx, time.Second*10)
		defer cancel()

		req, errReq := http.NewRequestWithContext(localCtx, http.MethodPost, remote.URL+"/api/server/auth", bytes.NewReader(body))
		if errReq != nil {
			return "", errors.Wrap(errEnc, "Failed to create request")
		}

		req.Header.Add("Content-Type", "application/json")

		response, errResp := client.Do(req)
		if errResp != nil {
			return "", errors.Wrap(errResp, "Failed to perform request")
		}

		defer func() {
			_ = response.Body.Close()
		}()

		respBody, errBody := io.ReadAll(response.Body)
		if errBody != nil {
			return "", errors.Wrap(errBody, "Failed to read response body")
		}

		var resp authResponse
		if errUnmarshal := json.Unmarshal(respBody, &resp); errUnmarshal != nil {
			return "", errors.Wrap(errUnmarshal, "Failed to unmarshal response body")
		}

		return resp.Token, nil
	}

	for index, remoteCfg := range config.Global.Remotes {
		token, errToken := fetchToken(remoteCfg)

		if errToken != nil {
			log.Error("Failed to fetch new token", zap.Error(errToken))

			continue
		}

		log.Info("Refreshed token successfully", zap.String("name", remoteCfg.Name))

		config.Global.Remotes[index].AuthToken = token
	}
}

func Start() {
	var (
		ctx                = context.Background()
		logger             = config.MustCreateLogger()
		fileScanTicker     = time.NewTicker(time.Second * 2)
		tokenRefreshTicker = time.NewTicker(time.Hour * 6)
	)

	for _, rules := range config.Global.Rules {
		logger.Info("Watching path",
			zap.String("root", rules.Root),
			zap.String("name", rules.Name),
			zap.String("remote", strings.Join(rules.Remotes, ",")))
	}

	updateChan := make(chan any)

	go func() {
		updateChan <- true
	}()

	for {
		select {
		case <-tokenRefreshTicker.C:
			updateChan <- true
		case <-updateChan:
			refreshTokens(ctx, logger)
		case <-fileScanTicker.C:
			if errCheck := update(ctx, logger, config.Global.Rules, config.Global.Remotes); errCheck != nil {
				logger.Error("Failed to update", zap.Error(errCheck))

				continue
			}
		case <-ctx.Done():
			logger.Info("Exiting")

			return
		}
	}
}
