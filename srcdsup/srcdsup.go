package srcdsup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/dustin/go-humanize"
	"github.com/helloyi/go-sshclient"
	"github.com/leighmacdonald/srcdsup/config"
	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	log "github.com/sirupsen/logrus"
	"io/fs"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type uploaderFunc func(ctx context.Context, ruleSet config.RulesConfig, conf config.RemoteConfig, files []fs.FileInfo) error

func update(ctx context.Context, rules []config.RulesConfig, remoteConfig []config.RemoteConfig, uploadHandlers map[config.RemoteServiceType]uploaderFunc) error {
	for _, ruleSet := range rules {
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
		// Newest first
		sort.Slice(fileInfo, func(i, j int) bool {
			return fileInfo[i].ModTime().After(fileInfo[j].ModTime())
		})
		// Ignore the current file
		if len(fileInfo) <= 1 {
			continue
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
func uploadSSH(_ context.Context, ruleSet config.RulesConfig, remoteConfig config.RemoteConfig, files []fs.FileInfo) error {
	sshClient, errClient := NewSSHClient(remoteConfig)
	if errClient != nil {
		return errClient
	}
	defer func() {
		if errDisc := sshClient.Close(); errDisc != nil {
			log.Errorf("Failed to close ssh connection cleanly: %v", errDisc)
		}
	}()
	sftpClient, sftpClientErr := sftp.NewClient(sshClient.UnderlyingClient())
	if sftpClientErr != nil {
		return sftpClientErr
	}
	if errMkDir := sftpClient.MkdirAll(remoteConfig.Root); errMkDir != nil {
		log.Fatalf("Cannot make dest dir: %v", errMkDir)
	}
	for _, file := range files[1:] {
		srcFile := filepath.Join(ruleSet.Root, file.Name())
		destFile := filepath.Join(remoteConfig.Root, file.Name())
		log.WithFields(log.Fields{
			"src":  srcFile,
			"dest": destFile,
			"size": file.Size(),
		}).Infof("Uploading SourceTV demo...")
		tStart := time.Now()
		of, errOf := sftpClient.Create(destFile)
		if errOf != nil {
			log.Fatalf("Error opening remote file: %v", errOf)
		}
		demoData, errReadDemoData := ioutil.ReadFile(srcFile)
		if errReadDemoData != nil {
			log.Fatalf("Failed to read demo data file: %v", errReadDemoData)
		}
		wroteCount, errWrite := of.Write(demoData)
		if errWrite != nil {
			log.Fatalf("Failed to write data: %v", errWrite)
		}
		log.WithFields(log.Fields{
			"written":  wroteCount,
			"duration": time.Since(tStart).String(),
		}).Infof("Upload successful")
		if errRemove := os.Remove(srcFile); errRemove != nil {
			log.WithFields(log.Fields{"src": srcFile}).Errorf("Error deleteing local demo file: %v", errRemove)
		} else {
			log.WithFields(log.Fields{"src": srcFile}).Infof("Deleted local demo file")
		}
	}
	return nil
}

func upload(ctx context.Context, rules config.RulesConfig, remoteConfigs []config.RemoteConfig, files []fs.FileInfo, uploadHandlers map[config.RemoteServiceType]uploaderFunc) error {
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
				handlerErr := handler(ctx, rules, rc, files)
				if handlerErr != nil {
					log.WithFields(log.Fields{
						"type": rc.Type,
						"name": rc.Name,
					}).Errorf("Upload handler error: %v", handlerErr)
					continue
				}
			}
		}
	}
	for _, filePath := range files {
		if errRemove := os.Remove(rules.SrcFile(filePath)); errRemove != nil {
			return errors.Wrapf(errRemove, "Could not cleanup source file")
		}
		log.WithFields(log.Fields{"src": filePath.Name()}).Infof("Removed source file")
	}
	return nil
}

// NewSSHClient returns a new connected ssh client.
// Close() must be called.
func NewSSHClient(config config.RemoteConfig) (*sshclient.Client, error) {
	if config.PrivateKeyPath != "" {
		if config.Password != "" {
			return sshclient.DialWithKeyWithPassphrase(config.Url, config.Username, config.PrivateKeyPath, config.Password)
		} else {
			// without passphrase
			return sshclient.DialWithKey(config.Url, config.Username, config.PrivateKeyPath)
		}
	} else {
		return sshclient.DialWithPasswd(config.Url, config.Username, config.Password)
	}
}

type ServerLogUpload struct {
	ServerName string                   `json:"server_name"`
	MapName    string                   `json:"map_name"`
	Body       string                   `json:"body"`
	Type       config.RemoteServiceType `json:"type"`
}

var mapName = regexp.MustCompile(`^auto-\d+-\d+-(?P<map>.+?)\.dem$`)

func uploadGbans(ctx context.Context, serviceType config.RemoteServiceType, ruleSet config.RulesConfig,
	remoteConfig config.RemoteConfig, files []fs.FileInfo) error {
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
		body, readErr := ioutil.ReadFile(ruleSet.SrcFile(f))
		if readErr != nil {
			cancel()
			return readErr
		}
		if len(body) <= 1250000 {
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
		})
		if encodeErr != nil {
			cancel()
			return errors.Wrapf(encodeErr, "Failed to encode request body")
		}
		req, errReq := http.NewRequestWithContext(localCtx, "POST", remoteConfig.Url, bytes.NewReader(request))
		if errReq != nil {
			cancel()
			return errors.Wrapf(errReq, "Failed to create request")
		}
		req.Header.Add("Authorization", remoteConfig.Password)
		req.Header.Add("Content-Type", "application/json")
		resp, errResp := client.Do(req)
		if errResp != nil {
			cancel()
			return errors.Wrapf(errResp, "Failed to upload entity")
		}
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
			respBody, errRespBody := ioutil.ReadAll(resp.Body)
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
		}).Debugf("Upload successful")
	}
	return nil
}

func uploadGbansType(t config.RemoteServiceType) uploaderFunc {
	return func(ctx context.Context, ruleSet config.RulesConfig, remoteConfig config.RemoteConfig, files []fs.FileInfo) error {
		return uploadGbans(ctx, t, ruleSet, remoteConfig, files)
	}
}

func Start() {
	var (
		ctx = context.Background()
		t0  = time.NewTicker(time.Second * 30)

		uploadHandlers = map[config.RemoteServiceType]uploaderFunc{
			config.SSH:          uploadSSH,
			config.GBansGameLog: uploadGbansType(config.GBansGameLog),
			config.GBansDemos:   uploadGbansType(config.GBansDemos),
		}
	)
	log.Infof("Starting srcdsup")
	for _, rules := range config.Global.Rules {
		log.WithFields(log.Fields{
			"root":   rules.Root,
			"name":   rules.Name,
			"remote": strings.Join(rules.Remotes, ","),
		}).Infof("Watching path")
	}
	for {
		select {
		case <-t0.C:
			log.Debugf("Update started")
			if errCheck := update(ctx, config.Global.Rules, config.Global.Remotes, uploadHandlers); errCheck != nil {
				log.Errorf("Failed to update: %v", errCheck)
				continue
			}
			log.Debugf("Update complete")
			t0.Reset(time.Second * 30)
		case <-ctx.Done():
			log.Infof("Exiting")
			return
		}
	}
}
