package srcdsup

import (
	"context"
	"fmt"
	"github.com/helloyi/go-sshclient"
	"github.com/leighmacdonald/srcdsup/config"
	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	log "github.com/sirupsen/logrus"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ServerLogUpload struct {
	ServerName string `json:"server_name"`
	Body       string `json:"body"`
}

func update(rules []config.RulesConfig, remoteConfig config.RemoteServerConfig) error {
	for _, ruleSet := range rules {
		fileInfo, rootErr := ioutil.ReadDir(ruleSet.LocalRoot)
		if rootErr != nil {
			return rootErr
		}
		var filteredFiles []fs.FileInfo
		for _, f := range fileInfo {
			if strings.HasSuffix(strings.ToLower(f.Name()), ruleSet.Suffix) {
				filteredFiles = append(filteredFiles, f)
			}
		}
		// Newest first
		sort.Slice(filteredFiles, func(i, j int) bool {
			return filteredFiles[i].ModTime().After(filteredFiles[j].ModTime())
		})
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
		if errUpload := upload(ruleSet, remoteConfig, filteredFiles); errUpload != nil {
			return errors.Wrapf(errUpload, "Failed to upload new match")
		}
	}

	return nil
}

func upload(rules config.RulesConfig, remoteConfig config.RemoteServerConfig, files []fs.FileInfo) error {
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
	if errMkDir := sftpClient.MkdirAll(rules.RemoteRoot); errMkDir != nil {
		log.Fatalf("Cannot make dest dir: %v", errMkDir)
	}
	for _, file := range files[1:] {
		srcFile := filepath.Join(rules.LocalRoot, file.Name())
		destFile := filepath.Join(rules.RemoteRoot, file.Name())
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

// NewSSHClient returns a new connected ssh client.
// Close() must be called.
func NewSSHClient(config config.RemoteServerConfig) (*sshclient.Client, error) {
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

func Start() {
	var (
		ctx = context.Background()
		t0  = time.NewTicker(time.Second * 5)
	)
	log.Infof("Starting stvup")
	for {
		select {
		case <-t0.C:
			log.Debugf("Update started")
			if errCheck := update(config.Global.Rules, config.Global.RemoteDest); errCheck != nil {
				log.Errorf("Failed to update: %v", errCheck)
			}
			log.Debugf("Update complete")
		case <-ctx.Done():
			log.Infof("Exiting")
		}
	}
}
