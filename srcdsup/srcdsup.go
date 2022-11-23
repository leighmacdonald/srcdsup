package srcdsup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/dustin/go-humanize"
	"github.com/leighmacdonald/srcdsup/config"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type uploaderFunc func(ctx context.Context, ruleSet *config.RulesConfig, conf *config.RemoteConfig, files []fs.FileInfo) error

func update(ctx context.Context, rules []*config.RulesConfig, remoteConfig []*config.RemoteConfig, uploadHandlers map[config.RemoteServiceType]uploaderFunc) error {
	for _, ruleSet := range rules {
		log.WithFields(log.Fields{"rule": ruleSet.Name}).Debug("Updating")
		matches, errGlob := filepath.Glob(path.Join(ruleSet.Root, ruleSet.Pattern))
		if errGlob != nil {
			log.Warnf("Error globbing files: %v", errGlob)
			continue
		}
		if matches == nil {
			continue
		}
		var fileInfo []fs.FileInfo
		for _, f := range matches {
			stat, errStat := os.Stat(f)
			if errStat != nil {
				log.Errorf("Could not read log file: %v", errStat)
				continue
			}
			fileInfo = append(fileInfo, stat)
		}

		for _, file := range fileInfo[1:] {
			log.WithFields(log.Fields{
				"rule": ruleSet.Name,
				"name": file.Name(),
				"size": file.Size(),
				"time": file.ModTime().String(),
			}).Infof("New rule match found")
		}
		if errUpload := upload(ctx, ruleSet, remoteConfig, fileInfo, uploadHandlers); errUpload != nil {
			return errors.Wrapf(errUpload, "Failed to upload new match")
		}
	}

	return nil
}

func upload(ctx context.Context, rules *config.RulesConfig, remoteConfigs []*config.RemoteConfig, files []fs.FileInfo, uploadHandlers map[config.RemoteServiceType]uploaderFunc) error {
	for _, rc := range remoteConfigs {
		for _, remoteRule := range rules.Remotes {
			if remoteRule == rc.Name {
				if rc.Name == "" {
					return errors.Errorf("Failed to find remote: %v", remoteRule)
				}
				handler, handlerFound := uploadHandlers[rc.Type]
				if !handlerFound {
					return errors.Errorf("No handler registered for type: %v", rc.Type)
				}
				if handlerErr := handler(ctx, rules, rc, files); handlerErr != nil {
					log.WithFields(log.Fields{
						"type": rc.Type,
						"name": rc.Name,
					}).Errorf("Upload handler error: %v", handlerErr)
					continue
				} else {
					log.Infof("Upload completed")
				}
			}
		}
	}
	for _, filePath := range files {
		if errRemove := os.RemoveAll(rules.SrcFile(filePath)); errRemove != nil {
			return errors.Wrapf(errRemove, "Could not cleanup source file")
		}
		log.WithFields(log.Fields{"src": filePath.Name()}).Infof("Removed source file")
	}
	return nil
}

type ServerLogUpload struct {
	ServerName string                   `json:"server_name"`
	MapName    string                   `json:"map_name"`
	Body       string                   `json:"body"`
	DemoName   string                   `json:"demo_name"`
	Type       config.RemoteServiceType `json:"type"`
}

var mapName = regexp.MustCompile(`^\S+-\d+-\d+-\d+-(?P<map>.+?)\.dem$`)

func uploadGbans(ctx context.Context, serviceType config.RemoteServiceType, ruleSet *config.RulesConfig,
	remoteConfig *config.RemoteConfig, files []fs.FileInfo) error {
	for _, f := range files {
		client := http.Client{Timeout: time.Second * 120}
		var demoMapName = ""
		localCtx, cancel := context.WithTimeout(ctx, time.Second*120)
		if serviceType == config.GBansDemos {
			matches := mapName.FindStringSubmatch(f.Name())
			if matches == nil {
				cancel()
				continue
			}
			demoMapName = matches[1]
		}
		body, readErr := os.ReadFile(ruleSet.SrcFile(f))
		if readErr != nil {
			cancel()
			return readErr
		}
		if len(body) <= 50000 {
			log.WithFields(log.Fields{"file": f.Name()}).Debugf("Skipping small file")
			cancel()
			continue
		}
		log.WithFields(log.Fields{
			"remote": remoteConfig.Name,
			"type":   remoteConfig.Type,
			"file":   f.Name(),
			"name":   remoteConfig.Name,
			"size":   humanize.Bytes(uint64(len(body))),
		}).Infof("Uploading file")
		request, encodeErr := json.Marshal(ServerLogUpload{
			ServerName: ruleSet.Server,
			Body:       base64.StdEncoding.EncodeToString(body),
			Type:       serviceType,
			MapName:    demoMapName,
			DemoName:   f.Name(),
		})
		if encodeErr != nil {
			cancel()
			return errors.Wrapf(encodeErr, "Failed to encode request body")
		}
		req, errReq := http.NewRequestWithContext(localCtx, "POST", remoteConfig.Url+"/api/demo", bytes.NewReader(request))
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
				log.Debugf("Error response: %v", respBody)
				if errClose := resp.Body.Close(); errClose != nil {
					log.Errorf("Failed to close response: %v", errClose)
				}
			}

			cancel()
			return errors.Errorf("Invalid status code: %s", string(respBody))
		}
		cancel()
		log.WithFields(log.Fields{
			"remote": remoteConfig.Name,
			"type":   remoteConfig.Type,
			"file":   f.Name(),
			"name":   remoteConfig.Name,
		}).Infof("Upload successful")
	}
	return nil
}

func uploadGbansType(t config.RemoteServiceType) uploaderFunc {
	return func(ctx context.Context, ruleSet *config.RulesConfig, remoteConfig *config.RemoteConfig, files []fs.FileInfo) error {
		return uploadGbans(ctx, t, ruleSet, remoteConfig, files)
	}
}

type gbansApiResponse struct {
	// Status is a simple truthy status of the response. See response codes for more specific
	// error handling scenarios
	Status  bool            `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error,omitempty"`
	Result  json.RawMessage `json:"result"`
}

func refreshTokens(ctx context.Context) {
	type authRequest struct {
		ServerName string `json:"server_name"`
		Key        string `json:"key"`
	}
	type authResponse struct {
		Token string `json:"token"`
	}
	var fetchToken = func(remote *config.RemoteConfig) (string, error) {
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
		req, errReq := http.NewRequestWithContext(localCtx, "POST", remote.Url+"/api/server/auth", bytes.NewReader(body))
		if errReq != nil {
			return "", errors.Wrap(errEnc, "Failed to create request")
		}
		req.Header.Add("Content-Type", "application/json")
		response, errResp := client.Do(req)
		if errResp != nil {
			return "", errors.Wrap(errResp, "Failed to perform request")
		}
		respBody, errBody := io.ReadAll(response.Body)
		if errBody != nil {
			return "", errors.Wrap(errBody, "Failed to read response body")
		}
		var resp gbansApiResponse
		if errUnmarshal := json.Unmarshal(respBody, &resp); errUnmarshal != nil {
			return "", errors.Wrap(errUnmarshal, "Failed to unmarshal response body")
		}
		var ar authResponse
		if errUnmarshal := json.Unmarshal(resp.Result, &ar); errUnmarshal != nil {
			return "", errors.Wrap(errUnmarshal, "Failed to unmarshal result body")
		}
		return ar.Token, nil
	}

	for i, remoteCfg := range config.Global.Remotes {
		token, errToken := fetchToken(remoteCfg)
		if errToken != nil {
			log.WithError(errToken).Errorf("Failed to fetch new token")
			continue
		}
		config.Global.Remotes[i].AuthToken = token
	}

}

func Start() {
	var (
		ctx            = context.Background()
		t0             = time.NewTicker(time.Second * 2)
		t1             = time.NewTicker(time.Hour * 6)
		uploadHandlers = map[config.RemoteServiceType]uploaderFunc{
			config.GBansDemos: uploadGbansType(config.GBansDemos),
		}
	)
	for _, rules := range config.Global.Rules {
		log.WithFields(log.Fields{
			"root":   rules.Root,
			"name":   rules.Name,
			"remote": strings.Join(rules.Remotes, ","),
		}).Infof("Watching path")
	}
	refreshTokens(ctx)
	for {
		select {
		case <-t1.C:
			refreshTokens(ctx)
		case <-t0.C:
			if errCheck := update(ctx, config.Global.Rules, config.Global.Remotes, uploadHandlers); errCheck != nil {
				log.Errorf("Failed to update: %v", errCheck)
				continue
			}
		case <-ctx.Done():
			log.Infof("Exiting")
			return
		}
	}
}
