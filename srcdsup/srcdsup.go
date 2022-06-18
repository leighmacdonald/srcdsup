package srcdsup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/helloyi/go-sshclient"
	"github.com/leighmacdonald/srcdsup/config"
	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	log "github.com/sirupsen/logrus"
	"io/fs"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type uploaderFunc func(ctx context.Context, ruleSet config.RulesConfig, conf config.RemoteConfig, files []fs.FileInfo) error

func update(ctx context.Context, rules []config.RulesConfig, remoteConfig []config.RemoteConfig, uploadHandlers map[config.RemoteServiceType]uploaderFunc) error {
	// TODO
	// - alternate upload types
	// - http remote sink
	for _, ruleSet := range rules {
		matches, errGlob := filepath.Glob(ruleSet.Src)
		if errGlob != nil {
			log.Warnf("Error globbing files: %v", errGlob)
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
		var filteredFiles []fs.FileInfo
		for _, f := range fileInfo {
			if strings.HasSuffix(strings.ToLower(f.Name()), "FIXME suffix") {
				filteredFiles = append(filteredFiles, f)
			}
		}
		// Newest first
		sort.Slice(filteredFiles, func(i, j int) bool {
			return filteredFiles[i].ModTime().After(filteredFiles[j].ModTime())
		})
		// Ignore the current file
		if len(filteredFiles) <= 1 {
			continue
		}
		for _, file := range filteredFiles[1:] {
			log.WithFields(log.Fields{
				"rule": ruleSet.Name,
				"name": file.Name(),
				"size": file.Size(),
				"time": file.ModTime().String(),
			}).Infof("New rule match found")
		}
		if errUpload := upload(ctx, ruleSet, remoteConfig, filteredFiles, uploadHandlers); errUpload != nil {
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
		srcFile := filepath.Join(ruleSet.Src, file.Name())
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
	var remoteConf config.RemoteConfig
	for _, rc := range remoteConfigs {
		if rules.Remote == rc.Name {
			remoteConf = rc
			break
		}
	}
	if remoteConf.Name == "" {
		return errors.Errorf("Failed to find remote: %v", rules.Remote)
	}
	handler, handlerFound := uploadHandlers[remoteConf.Type]
	if !handlerFound {
		return errors.Errorf("No handler registered for type: %v", remoteConf.Type)
	}
	return handler(ctx, rules, remoteConf, files)
}

// NewSSHClient returns a new connected ssh client.
// Close() must be called.
func NewSSHClient(config config.RemoteConfig) (*sshclient.Client, error) {
	var (
		addr = fmt.Sprintf("%s:%d", config.Host, config.Port)
	)
	if config.PrivateKeyPath != "" {
		if config.Password != "" {
			return sshclient.DialWithKeyWithPassphrase(addr, config.Username, config.PrivateKeyPath, config.Password)
		} else {
			// without passphrase
			return sshclient.DialWithKey(addr, config.Username, config.PrivateKeyPath)
		}
	} else {
		return sshclient.DialWithPasswd(addr, config.Username, config.Password)
	}
}

type serverLogUpload struct {
	ServerName string                   `json:"server_name"`
	Body       string                   `json:"body"`
	Type       config.RemoteServiceType `json:"type"`
}

func uploadGbansReal(ctx context.Context, typ config.RemoteServiceType, ruleSet config.RulesConfig,
	remoteConfig config.RemoteConfig, files []fs.FileInfo) error {
	client := http.Client{Timeout: time.Minute * 30}
	url := fmt.Sprintf("https://%s:%d/%s", remoteConfig.Host, remoteConfig.Port, remoteConfig.Path)
	for _, f := range files {
		localCtx, cancel := context.WithTimeout(ctx, time.Second*30)
		srcFile := filepath.Join(ruleSet.Src, f.Name())
		body, readErr := ioutil.ReadFile(srcFile)
		if readErr != nil {
			cancel()
			return readErr
		}
		request, encodeErr := json.Marshal(serverLogUpload{
			ServerName: remoteConfig.Name,
			Body:       base64.StdEncoding.EncodeToString(body),
			Type:       typ,
		})
		if encodeErr != nil {
			cancel()
			return errors.Wrapf(encodeErr, "Failed to encode request body")
		}
		req, errReq := http.NewRequestWithContext(localCtx, "POST", url, bytes.NewReader(request))
		if errReq != nil {
			cancel()
			return errors.Wrapf(errReq, "Failed to create request")
		}
		req.Header.Add("Authorization", remoteConfig.Password)
		resp, errResp := client.Do(req)
		if errResp != nil {
			cancel()
			return errors.Wrapf(errResp, "Failed to upload entity")
		}

		if resp.StatusCode != http.StatusCreated {
			log.Errorf("Invalid response code: %d", resp.StatusCode)
			cancel()
			return errors.New("Invalid status code")
		}
		cancel()
	}
	return nil
}

func uploadGbansType(t config.RemoteServiceType) uploaderFunc {
	return func(ctx context.Context, ruleSet config.RulesConfig, remoteConfig config.RemoteConfig, files []fs.FileInfo) error {
		return uploadGbansReal(ctx, t, ruleSet, remoteConfig, files)
	}
}

func Start() {
	var (
		ctx = context.Background()
		t0  = time.NewTicker(time.Second * 5)

		uploadHandlers = map[config.RemoteServiceType]uploaderFunc{
			config.SSH:          uploadSSH,
			config.GBansGameLog: uploadGbansType(config.GBansGameLog),
			config.GBansDemos:   uploadGbansType(config.GBansDemos),
		}
	)
	log.Infof("Starting srcdsup")
	for _, rules := range config.Global.Rules {
		log.WithFields(log.Fields{
			"src":    rules.Src,
			"name":   rules.Name,
			"remote": rules.Remote,
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
		case <-ctx.Done():
			log.Infof("Exiting")
			return
		}
	}
}
