package srcdsup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/leighmacdonald/srcdsup/config"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
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

func uploadGbans(ctx context.Context, log *zap.Logger, serviceType config.RemoteServiceType, ruleSet *config.RulesConfig,
	remoteConfig *config.RemoteConfig, files []fs.FileInfo,
) error {
	for _, curFile := range files {
		client := http.Client{Timeout: time.Second * 120}
		localCtx, cancel := context.WithTimeout(ctx, time.Second*120)

		if serviceType != config.GBansDemos {
			cancel()

			return errors.New("Invalid service type")
		}

		jsonPath, pathErr := filepath.Abs(strings.Join([]string{ruleSet.SrcFile(curFile), "json"}, "."))
		if pathErr != nil {
			cancel()

			return errors.Wrap(pathErr, "Failed to build path")
		}

		jsonBody, readErrJSON := os.ReadFile(jsonPath)
		if readErrJSON != nil {
			cancel()

			return errors.Wrap(readErrJSON, "Failed to read json file")
		}

		var stats metaData
		if errEnc := json.Unmarshal(jsonBody, &stats); errEnc != nil {
			cancel()

			return errors.Wrap(errEnc, "Failed to decode stats")
		}

		body, readErr := os.ReadFile(ruleSet.SrcFile(curFile))
		if readErr != nil {
			cancel()

			return errors.Wrap(readErr, "Failed to read file")
		}

		if len(body) <= 50000 {
			log.Info("Skipping small file", zap.String("file", curFile.Name()))
			cancel()

			continue
		}

		log.Info("Uploading file",
			zap.String("remote", remoteConfig.Name),
			zap.String("type", string(remoteConfig.Type)),
			zap.String("file", curFile.Name()),
			zap.String("name", remoteConfig.Name),
			zap.String("size", humanize.Bytes(uint64(len(body)))))

		payload := ServerLogUpload{
			ServerName: ruleSet.Server,
			Body:       base64.StdEncoding.EncodeToString(body),
			Type:       serviceType,
			MapName:    stats.MapName,
			DemoName:   curFile.Name(),
			Scores:     stats.Scores,
		}

		request, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			cancel()

			return errors.Wrapf(encodeErr, "Failed to encode request body")
		}

		req, errReq := http.NewRequestWithContext(localCtx, http.MethodPost, remoteConfig.URL+"/api/demo", bytes.NewReader(request))
		if errReq != nil {
			cancel()

			return errors.Wrapf(errReq, "Failed to create request")
		}

		req.Header.Add("Authorization", remoteConfig.AuthToken)
		req.Header.Add("Content-Type", "application/json")

		resp, errResp := client.Do(req)
		if errResp != nil {
			cancel()

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

			cancel()

			return errors.Errorf("Invalid status code: %s", string(respBody))
		}

		cancel()

		log.Info("Uploading successful",
			zap.String("remote", remoteConfig.Name),
			zap.String("type", string(remoteConfig.Type)),
			zap.String("file", curFile.Name()),
			zap.String("name", remoteConfig.Name),
			zap.String("size", humanize.Bytes(uint64(len(body)))))
	}

	return nil
}

func uploadGbansType(t config.RemoteServiceType, log *zap.Logger) uploaderFunc {
	return func(ctx context.Context, ruleSet *config.RulesConfig, remoteConfig *config.RemoteConfig, files []fs.FileInfo) error {
		return uploadGbans(ctx, log, t, ruleSet, remoteConfig, files)
	}
}

func refreshTokens(ctx context.Context) {
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
			log.WithError(errToken).Errorf("Failed to fetch new token")

			continue
		}

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
			refreshTokens(ctx)
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
