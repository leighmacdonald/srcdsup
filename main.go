package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/helloyi/go-sshclient"
	"github.com/pkg/sftp"
	log "github.com/sirupsen/logrus"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	PrivateKeyPath string
	Username       string
	Password       string
	LocalRoot      string
	RemoteRoot     string
	Host           string
	Port           int
}

func readConfig(config *Config) error {
	privateKeyPath, errPrivateKeyPath := os.LookupEnv("STV_PRIVATE_KEY")
	if !errPrivateKeyPath || privateKeyPath == "" {
		return errors.New("must set STV_PRIVATE_KEY")
	}
	userName, errUserName := os.LookupEnv("STV_USERNAME")
	if !errUserName || userName == "" {
		return errors.New("must set STV_USERNAME")
	}
	host, errHost := os.LookupEnv("STV_HOST")
	if !errHost || host == "" {
		return errors.New("must set STV_HOST")
	}
	remoteRoot, errRemoteRoot := os.LookupEnv("STV_REMOTE_ROOT")
	if !errRemoteRoot || remoteRoot == "" {
		return errors.New("must set STV_REMOTE_ROOT")
	}
	localRoot, errLocalRoot := os.LookupEnv("STV_LOCAL_ROOT")
	if !errLocalRoot || localRoot == "" {
		return errors.New("must set STV_LOCAL_ROOT")
	}
	port := 22
	portStr, errPort := os.LookupEnv("STV_PORT")
	if errPort && portStr == "" {
		log.Warnf("Using default ssh port")
		portParsed, errPortParse := strconv.ParseUint(portStr, 10, 16)
		if errPortParse != nil {
			return errPortParse
		}
		port = int(portParsed)
	}
	passPhrase, _ := os.LookupEnv("STV_PASSWORD")

	config.PrivateKeyPath = privateKeyPath
	config.Username = userName
	config.Password = passPhrase
	config.RemoteRoot = remoteRoot
	config.LocalRoot = localRoot
	config.Host = host
	config.Port = port

	return nil
}

func Do(config *Config) error {
	fileInfo, rootErr := ioutil.ReadDir(config.LocalRoot)
	if rootErr != nil {
		return rootErr
	}
	// Only look at .dem files
	var filtered []fs.FileInfo
	for _, f := range fileInfo {
		if strings.HasSuffix(strings.ToLower(f.Name()), ".dem") {
			filtered = append(filtered, f)
		}
	}
	// Newest first
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].ModTime().After(filtered[j].ModTime())
	})
	for i, file := range filtered {
		if i == 0 {
			// Skip the current active recording
			continue
		}
		log.WithFields(log.Fields{
			"name": file.Name(),
			"size": file.Size(),
			"time": file.ModTime(),
		}).Print("New demo found")
	}
	if len(filtered) > 1 {
		sshClient, errClient := NewSSHClient(config)
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
		if errMkDir := sftpClient.MkdirAll(config.RemoteRoot); errMkDir != nil {
			log.Fatalf("Cannot make dest dir: %v", errMkDir)
		}
		for _, file := range filtered {
			srcFile := filepath.Join(config.LocalRoot, file.Name())
			destFile := filepath.Join(config.RemoteRoot, file.Name())
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
				log.WithFields(log.Fields{"src": srcFile}).Debugf("Deleted local demo file")
			}
		}
	}
	return nil
}

// NewSSHClient returns a new connected ssh client.
// Close() must be called.
func NewSSHClient(config *Config) (*sshclient.Client, error) {
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

func main() {
	var (
		config Config
		ctx    = context.Background()
		t0     = time.NewTicker(time.Second * 5)
	)
	if errConfig := readConfig(&config); errConfig != nil {
		log.Fatalf("Error reading config: %v", errConfig)
	}
	for {
		select {
		case <-t0.C:
			if errCheck := Do(&config); errCheck != nil {
				log.Errorf("Failed to update: %v", errCheck)
			}
		case <-ctx.Done():
			log.Infof("Exiting")
		}
	}
}
