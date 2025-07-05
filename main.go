package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type FileSecretSync struct {
	client       kubernetes.Interface
	namespace    string
	folderPath   string
	secretName   string
	watcher      *fsnotify.Watcher
}

func main() {
	// Read environment variables
	folderToRead := os.Getenv("folder_to_read")
	if folderToRead == "" {
		log.Fatal("folder_to_read environment variable is required")
	}

	secretToWrite := os.Getenv("secret_to_write")
	if secretToWrite == "" {
		log.Fatal("secret_to_write environment variable is required")
	}

	// Get current namespace from service account
	namespace, err := getCurrentNamespace()
	if err != nil {
		log.Fatalf("Failed to get current namespace: %v", err)
	}

	// Create in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Failed to create in-cluster config: %v", err)
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create clientset: %v", err)
	}

	// Create file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Failed to create file watcher: %v", err)
	}
	defer watcher.Close()

	// Initialize FileSecretSync
	fss := &FileSecretSync{
		client:     clientset,
		namespace:  namespace,
		folderPath: folderToRead,
		secretName: secretToWrite,
		watcher:    watcher,
	}

	// Perform initial sync
	log.Printf("Starting file-to-secret sync for folder: %s, secret: %s/%s", folderToRead, namespace, secretToWrite)
	if err := fss.syncFiles(); err != nil {
		log.Fatalf("Initial sync failed: %v", err)
	}

	// Start monitoring
	if err := fss.startMonitoring(); err != nil {
		log.Fatalf("Failed to start monitoring: %v", err)
	}
}

func getCurrentNamespace() (string, error) {
	// Read namespace from service account token
	namespaceBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", fmt.Errorf("failed to read namespace: %w", err)
	}
	return strings.TrimSpace(string(namespaceBytes)), nil
}

func (fss *FileSecretSync) syncFiles() error {
	log.Printf("Reading files from folder: %s", fss.folderPath)
	
	// Read all files from the folder
	data, err := fss.readFolderContents()
	if err != nil {
		return fmt.Errorf("failed to read folder contents: %w", err)
	}

	if len(data) == 0 {
		log.Printf("No files found in folder: %s", fss.folderPath)
		return nil
	}

	// Get existing secret
	ctx := context.Background()
	secret, err := fss.client.CoreV1().Secrets(fss.namespace).Get(ctx, fss.secretName, metav1.GetOptions{})
	
	if errors.IsNotFound(err) {
		// Create new secret
		return fss.createSecret(ctx, data)
	} else if err != nil {
		return fmt.Errorf("failed to get secret: %w", err)
	}

	// Update existing secret if data has changed
	if fss.hasDataChanged(secret.Data, data) {
		return fss.updateSecret(ctx, secret, data)
	}

	log.Printf("Secret %s is up to date", fss.secretName)
	return nil
}

func (fss *FileSecretSync) readFolderContents() (map[string][]byte, error) {
	data := make(map[string][]byte)

	err := filepath.WalkDir(fss.folderPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}

		// Use relative path as key
		relPath, err := filepath.Rel(fss.folderPath, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", path, err)
		}

		// Replace path separators with dots for secret key naming
		key := strings.ReplaceAll(relPath, string(filepath.Separator), ".")
		data[key] = content
		
		log.Printf("Read file: %s -> %s (%d bytes)", path, key, len(content))
		return nil
	})

	return data, err
}

func (fss *FileSecretSync) createSecret(ctx context.Context, data map[string][]byte) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fss.secretName,
			Namespace: fss.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "file-secret-sync",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}

	_, err := fss.client.CoreV1().Secrets(fss.namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	log.Printf("Created secret %s with %d files", fss.secretName, len(data))
	return nil
}

func (fss *FileSecretSync) updateSecret(ctx context.Context, secret *corev1.Secret, data map[string][]byte) error {
	secret.Data = data
	
	_, err := fss.client.CoreV1().Secrets(fss.namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}

	log.Printf("Updated secret %s with %d files", fss.secretName, len(data))
	return nil
}

func (fss *FileSecretSync) hasDataChanged(oldData, newData map[string][]byte) bool {
	if len(oldData) != len(newData) {
		return true
	}

	for key, newValue := range newData {
		oldValue, exists := oldData[key]
		if !exists || string(oldValue) != string(newValue) {
			return true
		}
	}

	return false
}

func (fss *FileSecretSync) startMonitoring() error {
	log.Printf("Starting file system monitoring for: %s", fss.folderPath)

	// Add the folder to the watcher
	err := fss.watcher.Add(fss.folderPath)
	if err != nil {
		return fmt.Errorf("failed to add folder to watcher: %w", err)
	}

	// Also watch subdirectories
	err = filepath.WalkDir(fss.folderPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != fss.folderPath {
			return fss.watcher.Add(path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to add subdirectories to watcher: %w", err)
	}

	// Debounce rapid file changes
	debounceTimer := time.NewTimer(0)
	<-debounceTimer.C // drain the timer

	for {
		select {
		case event, ok := <-fss.watcher.Events:
			if !ok {
				log.Println("Watcher closed")
				return nil
			}

			log.Printf("File event: %s %s", event.Op, event.Name)

			// Handle directory creation (need to add new dirs to watcher)
			if event.Op&fsnotify.Create == fsnotify.Create {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					log.Printf("Adding new directory to watcher: %s", event.Name)
					fss.watcher.Add(event.Name)
				}
			}

			// Debounce: reset timer on each event
			debounceTimer.Reset(1 * time.Second)

		case err, ok := <-fss.watcher.Errors:
			if !ok {
				log.Println("Watcher error channel closed")
				return nil
			}
			log.Printf("Watcher error: %v", err)

		case <-debounceTimer.C:
			// Debounce timer expired, sync files
			log.Println("Debounce timer expired, syncing files...")
			if err := fss.syncFiles(); err != nil {
				log.Printf("Sync failed: %v", err)
			}
		}
	}
}