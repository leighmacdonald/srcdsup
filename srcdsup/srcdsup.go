package srcdsup

import (
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
	"github.com/ulikunitz/xz"
	"go.uber.org/zap"
)

type uploaderFunc func(ctx context.Context, ruleSet *config.RulesConfig, conf *config.RemoteConfig, files []fs.FileInfo) error

func update(ctx context.Context, log *zap.Logger, rules []*config.RulesConfig, remoteConfig []*config.RemoteConfig, uploadHandlers map[config.RemoteServiceType]uploaderFunc) error {
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

			jsonPath, jsonPathErr := filepath.Abs(strings.Join([]string{fileMatch, "json"}, "."))
			if jsonPathErr != nil {
				return errors.Wrap(jsonPathErr, "Failed to build json file path")
			}

			if _, errStatJSON := os.Stat(jsonPath); os.IsNotExist(errStatJSON) {
				return errors.Wrapf(errStatJSON, "Cant find json metadata")
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

		if errUpload := upload(ctx, log, ruleSet, remoteConfig, fileCollection, uploadHandlers); errUpload != nil {
			return errors.Wrapf(errUpload, "Failed to upload new match")
		}
	}

	return nil
}

func upload(ctx context.Context, log *zap.Logger, rules *config.RulesConfig, remoteConfigs []*config.RemoteConfig, files []fs.FileInfo, uploadHandlers map[config.RemoteServiceType]uploaderFunc) error {
	for _, remoteConfig := range remoteConfigs {
		for _, remoteRule := range rules.Remotes {
			if remoteRule == remoteConfig.Name {
				if remoteConfig.Name == "" {
					return errors.Errorf("Failed to find remote: %v", remoteRule)
				}

				handler, handlerFound := uploadHandlers[remoteConfig.Type]
				if !handlerFound {
					return errors.Errorf("No handler registered for type: %v", remoteConfig.Type)
				}

				if handlerErr := handler(ctx, rules, remoteConfig, files); handlerErr != nil {
					log.Error("Upload handler error",
						zap.String("type", string(remoteConfig.Type)),
						zap.String("name", remoteConfig.Name),
						zap.Error(handlerErr))

					continue
				} else {
					log.Info("Upload completed successfully")
				}
			}
		}
	}

	for _, filePath := range files {
		srcFile := rules.SrcFile(filePath)
		if errRemove := os.RemoveAll(srcFile); errRemove != nil {
			return errors.Wrapf(errRemove, "Could not cleanup source file")
		}

		log.Debug("Removing source file", zap.String("path", srcFile))

		jsonPath := strings.Join([]string{srcFile, "json"}, ".")
		if errRemove := os.RemoveAll(jsonPath); errRemove != nil {
			return errors.Wrapf(errRemove, "Could not cleanup source file")
		}
	}

	return nil
}

type PlayerStats struct {
	Score      int `json:"score"`
	ScoreTotal int `json:"score_total"`
	Deaths     int `json:"deaths"`
}

type ServerLogUpload struct {
	ServerName string                   `json:"server_name"`
	MapName    string                   `json:"map_name"`
	Body       string                   `json:"body"`
	DemoName   string                   `json:"demo_name"`
	Type       config.RemoteServiceType `json:"type"`
	Scores     map[string]PlayerStats   `json:"scores"`
}

type metaData struct {
	MapName string                 `json:"map_name"`
	Scores  map[string]PlayerStats `json:"scores"`
}

// var mapName = regexp.MustCompile(`^\S+-\d+-\d+-\d+-(?P<map>.+?)\.dem$`)

func send(ctx context.Context, log *zap.Logger, serviceType config.RemoteServiceType, ruleSet *config.RulesConfig,
	curFile fs.FileInfo, remoteConfig *config.RemoteConfig,
) error {
	client := http.Client{Timeout: time.Second * 120}

	localCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	if serviceType != config.GBansDemos {
		return errors.New("Invalid service type")
	}

	jsonPath, pathErr := filepath.Abs(strings.Join([]string{ruleSet.SrcFile(curFile), "json"}, "."))
	if pathErr != nil {
		return errors.Wrap(pathErr, "Failed to build path")
	}

	jsonBody, readErrJSON := os.ReadFile(jsonPath)
	if readErrJSON != nil {
		return errors.Wrap(readErrJSON, "Failed to read json file")
	}

	var meta metaData
	if errEnc := json.Unmarshal(jsonBody, &meta); errEnc != nil {
		return errors.Wrap(errEnc, "Failed to decode stats")
	}

	body, readErr := os.ReadFile(ruleSet.SrcFile(curFile))
	if readErr != nil {
		return errors.Wrap(readErr, "Failed to read file")
	}

	if len(body) <= 50000 {
		return errors.Errorf("Skipping small file %s", curFile.Name())
	}

	var compressedDemo bytes.Buffer
	demoBufWriter := bufio.NewWriter(&compressedDemo)

	writer, errWriter := xz.NewWriter(demoBufWriter)
	if errWriter != nil {
		return errors.Wrap(errWriter, "xz.NewWriter error")
	}

	if _, errWrite := writer.Write(body); errWrite != nil {
		return errors.Wrap(errWrite, "Failed to write body to xz")
	}

	if errClose := writer.Close(); errClose != nil {
		return errors.Wrap(errClose, "Failed to close writer")
	}

	var (
		outBuffer       = new(bytes.Buffer)
		multiPartWriter = multipart.NewWriter(outBuffer)
	)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="demo"; filename="%s"`, curFile.Name()))

	fileWriter, errCreatePart := multiPartWriter.CreatePart(h)
	if errCreatePart != nil {
		return errors.Wrap(errCreatePart, "Failed to create part")
	}

	if _, errWrite := fileWriter.Write(compressedDemo.Bytes()); errWrite != nil {
		return errors.Wrap(errWrite, "Failed to write demo part")
	}

	if errWriteStats := multiPartWriter.WriteField("server_name", ruleSet.Server); errWriteStats != nil {
		return errors.Wrap(errWriteStats, "Failed to write server_name part")
	}

	if errWriteStats := multiPartWriter.WriteField("map_name", meta.MapName); errWriteStats != nil {
		return errors.Wrap(errWriteStats, "Failed to write map_name part")
	}

	if errWriteStats := multiPartWriter.WriteField("stats", string(jsonBody)); errWriteStats != nil {
		return errors.Wrap(errWriteStats, "Failed to write stats part")
	}

	if errClose := multiPartWriter.Close(); errClose != nil {
		return errors.Wrap(errClose, "Failed to close multipart writer")
	}

	req, errReq := http.NewRequestWithContext(localCtx, http.MethodPost, remoteConfig.URL+"/api/demo", outBuffer)
	if errReq != nil {
		return errors.Wrapf(errReq, "Failed to create request")
	}

	req.Header.Set("Authorization", remoteConfig.AuthToken)
	req.Header.Set("Content-Type", multiPartWriter.FormDataContentType())

	resp, errResp := client.Do(req)
	if errResp != nil {
		return errors.Wrapf(errResp, "Failed to upload entity")
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		respBody, errRespBody := io.ReadAll(resp.Body)
		if errRespBody == nil {
			log.Error("Upload gbans error", zap.String("body", string(respBody)))

			if errClose := resp.Body.Close(); errClose != nil {
				log.Error("Failed to close response", zap.Error(errClose))
			}
		}

		return errors.Errorf("Invalid status code: %d", resp.StatusCode)
	}

	return nil
}

func uploadGbans(ctx context.Context, log *zap.Logger, serviceType config.RemoteServiceType, ruleSet *config.RulesConfig,
	remoteConfig *config.RemoteConfig, files []fs.FileInfo,
) error {
	for _, curFile := range files {
		log.Info("Uploading file",
			zap.String("remote", remoteConfig.Name),
			zap.String("type", string(remoteConfig.Type)),
			zap.String("file", curFile.Name()),
			zap.String("name", remoteConfig.Name))

		if errSend := send(ctx, log, serviceType, ruleSet, curFile, remoteConfig); errSend != nil {
			log.Error("Failed to send file", zap.Error(errSend))

			continue
		}

		log.Info("Uploading successful",
			zap.String("remote", remoteConfig.Name),
			zap.String("type", string(remoteConfig.Type)),
			zap.String("file", curFile.Name()),
			zap.String("name", remoteConfig.Name))
	}

	return nil
}

func uploadGbansType(t config.RemoteServiceType, log *zap.Logger) uploaderFunc {
	return func(ctx context.Context, ruleSet *config.RulesConfig, remoteConfig *config.RemoteConfig, files []fs.FileInfo) error {
		return uploadGbans(ctx, log, t, ruleSet, remoteConfig, files)
	}
}

func refreshTokens(ctx context.Context, log *zap.Logger) {
	type authRequest struct {
		ServerName string `json:"server_name"`
		Key        string `json:"key"`
	}

	type authResponse struct {
		Token string `json:"token"`
	}

	fetchToken := func(remote *config.RemoteConfig) (string, error) {
		client := http.Client{Timeout: time.Second * 120}
		body, errEnc := json.Marshal(authRequest{
			ServerName: remote.Name,
			Key:        remote.Password,
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
		uploadHandlers     = map[config.RemoteServiceType]uploaderFunc{
			config.GBansDemos: uploadGbansType(config.GBansDemos, logger),
		}
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
			if errCheck := update(ctx, logger, config.Global.Rules, config.Global.Remotes, uploadHandlers); errCheck != nil {
				logger.Error("Failed to update", zap.Error(errCheck))

				continue
			}
		case <-ctx.Done():
			logger.Info("Exiting")

			return
		}
	}
}
