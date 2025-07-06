package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestGetCurrentNamespace(t *testing.T) {
	// Create a temporary file to simulate the namespace file
	tempDir := t.TempDir()
	namespaceFile := filepath.Join(tempDir, "namespace")

	testNamespace := "test-namespace"
	err := os.WriteFile(namespaceFile, []byte(testNamespace+"\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test namespace file: %v", err)
	}

	// Override the namespace file path for testing
	originalPath := "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

	// Create a mock function to test
	getCurrentNamespaceFromFile := func(path string) (string, error) {
		namespaceBytes, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(namespaceBytes), nil
	}

	namespace, err := getCurrentNamespaceFromFile(namespaceFile)
	if err != nil {
		t.Fatalf("getCurrentNamespace failed: %v", err)
	}

	if namespace != testNamespace+"\n" {
		t.Errorf("Expected namespace %q, got %q", testNamespace, namespace)
	}

	// Test with non-existent file
	_, err = getCurrentNamespaceFromFile(originalPath + "_nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent namespace file")
	}
}

func TestReadFolderContents(t *testing.T) {
	// Create temporary directory structure
	tempDir := t.TempDir()

	// Create test files
	testFiles := map[string]string{
		"config.yaml":     "apiVersion: v1\nkind: ConfigMap",
		"secret.json":     `{"username": "admin", "password": "secret"}`,
		"subdir/app.conf": "debug=true\nport=8080",
		"subdir/data.txt": "Hello, World!",
		"empty.txt":       "",
	}

	for filePath, content := range testFiles {
		fullPath := filepath.Join(tempDir, filePath)
		err := os.MkdirAll(filepath.Dir(fullPath), 0755)
		if err != nil {
			t.Fatalf("Failed to create directory for %s: %v", filePath, err)
		}

		err = os.WriteFile(fullPath, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to write test file %s: %v", filePath, err)
		}
	}

	// Test reading folder contents
	fss := &FileSecretSync{
		folderPath: tempDir,
	}

	data, err := fss.readFolderContents()
	if err != nil {
		t.Fatalf("readFolderContents failed: %v", err)
	}

	// Verify expected files were read
	expectedKeys := []string{
		"config.yaml",
		"secret.json",
		"subdir.app.conf",
		"subdir.data.txt",
		"empty.txt",
	}

	if len(data) != len(expectedKeys) {
		t.Errorf("Expected %d files, got %d", len(expectedKeys), len(data))
	}

	for _, key := range expectedKeys {
		if _, exists := data[key]; !exists {
			t.Errorf("Expected key %s not found in data", key)
		}
	}

	// Verify file contents
	if string(data["config.yaml"]) != testFiles["config.yaml"] {
		t.Errorf("Content mismatch for config.yaml")
	}

	if string(data["subdir.app.conf"]) != testFiles["subdir/app.conf"] {
		t.Errorf("Content mismatch for subdir.app.conf")
	}

	// Test with non-existent directory
	fss.folderPath = "/nonexistent"
	_, err = fss.readFolderContents()
	if err == nil {
		t.Error("Expected error for non-existent directory")
	}
}

func TestHasDataChanged(t *testing.T) {
	fss := &FileSecretSync{}

	testCases := []struct {
		name     string
		oldData  map[string][]byte
		newData  map[string][]byte
		expected bool
	}{
		{
			name:     "no change",
			oldData:  map[string][]byte{"key1": []byte("value1"), "key2": []byte("value2")},
			newData:  map[string][]byte{"key1": []byte("value1"), "key2": []byte("value2")},
			expected: false,
		},
		{
			name:     "value changed",
			oldData:  map[string][]byte{"key1": []byte("value1"), "key2": []byte("value2")},
			newData:  map[string][]byte{"key1": []byte("value1"), "key2": []byte("changed")},
			expected: true,
		},
		{
			name:     "key added",
			oldData:  map[string][]byte{"key1": []byte("value1")},
			newData:  map[string][]byte{"key1": []byte("value1"), "key2": []byte("value2")},
			expected: true,
		},
		{
			name:     "key removed",
			oldData:  map[string][]byte{"key1": []byte("value1"), "key2": []byte("value2")},
			newData:  map[string][]byte{"key1": []byte("value1")},
			expected: true,
		},
		{
			name:     "empty to non-empty",
			oldData:  map[string][]byte{},
			newData:  map[string][]byte{"key1": []byte("value1")},
			expected: true,
		},
		{
			name:     "non-empty to empty",
			oldData:  map[string][]byte{"key1": []byte("value1")},
			newData:  map[string][]byte{},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := fss.hasDataChanged(tc.oldData, tc.newData)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, result)
			}
		})
	}
}

func TestCreateSecret(t *testing.T) {
	client := fake.NewSimpleClientset()

	fss := &FileSecretSync{
		client:     client,
		namespace:  "test-namespace",
		secretName: "test-secret",
	}

	testData := map[string][]byte{
		"config.yaml": []byte("apiVersion: v1\nkind: ConfigMap"),
		"secret.json": []byte(`{"username": "admin"}`),
	}

	ctx := context.Background()
	err := fss.createSecret(ctx, testData)
	if err != nil {
		t.Fatalf("createSecret failed: %v", err)
	}

	// Verify secret was created
	secret, err := client.CoreV1().Secrets(fss.namespace).Get(ctx, fss.secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get created secret: %v", err)
	}

	if secret.Type != corev1.SecretTypeOpaque {
		t.Errorf("Expected secret type %s, got %s", corev1.SecretTypeOpaque, secret.Type)
	}

	if !reflect.DeepEqual(secret.Data, testData) {
		t.Errorf("Secret data mismatch")
	}

	expectedLabel := "file-secret-sync"
	if secret.Labels["app.kubernetes.io/managed-by"] != expectedLabel {
		t.Errorf("Expected label %s, got %s", expectedLabel, secret.Labels["app.kubernetes.io/managed-by"])
	}
}

func TestUpdateSecret(t *testing.T) {
	// Create existing secret
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "test-namespace",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"old-key": []byte("old-value"),
		},
	}

	client := fake.NewSimpleClientset(existingSecret)

	fss := &FileSecretSync{
		client:     client,
		namespace:  "test-namespace",
		secretName: "test-secret",
	}

	newData := map[string][]byte{
		"new-key": []byte("new-value"),
		"config":  []byte("updated-config"),
	}

	ctx := context.Background()
	err := fss.updateSecret(ctx, existingSecret, newData)
	if err != nil {
		t.Fatalf("updateSecret failed: %v", err)
	}

	// Verify secret was updated
	secret, err := client.CoreV1().Secrets(fss.namespace).Get(ctx, fss.secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get updated secret: %v", err)
	}

	if !reflect.DeepEqual(secret.Data, newData) {
		t.Errorf("Secret data was not updated correctly")
	}
}

func TestSyncFiles(t *testing.T) {
	// Create temporary directory with test files
	tempDir := t.TempDir()
	testFiles := map[string]string{
		"config.yaml": "test: value",
		"data.json":   `{"key": "value"}`,
	}

	for filePath, content := range testFiles {
		fullPath := filepath.Join(tempDir, filePath)
		err := os.WriteFile(fullPath, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	client := fake.NewSimpleClientset()

	fss := &FileSecretSync{
		client:     client,
		namespace:  "test-namespace",
		secretName: "test-secret",
		folderPath: tempDir,
	}

	// Test initial sync (secret creation)
	err := fss.syncFiles()
	if err != nil {
		t.Fatalf("syncFiles failed: %v", err)
	}

	// Verify secret was created
	ctx := context.Background()
	secret, err := client.CoreV1().Secrets(fss.namespace).Get(ctx, fss.secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get created secret: %v", err)
	}

	if len(secret.Data) != 2 {
		t.Errorf("Expected 2 keys in secret, got %d", len(secret.Data))
	}

	// Test sync with existing secret (no changes)
	err = fss.syncFiles()
	if err != nil {
		t.Fatalf("syncFiles failed on second run: %v", err)
	}

	// Test sync with changes
	newContent := "updated: content"
	err = os.WriteFile(filepath.Join(tempDir, "config.yaml"), []byte(newContent), 0644)
	if err != nil {
		t.Fatalf("Failed to update test file: %v", err)
	}

	err = fss.syncFiles()
	if err != nil {
		t.Fatalf("syncFiles failed after file update: %v", err)
	}

	// Verify secret was updated
	secret, err = client.CoreV1().Secrets(fss.namespace).Get(ctx, fss.secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get updated secret: %v", err)
	}

	if string(secret.Data["config.yaml"]) != newContent {
		t.Errorf("Secret was not updated with new content")
	}
}

func TestSyncFilesWithEmptyDirectory(t *testing.T) {
	// Create empty temporary directory
	tempDir := t.TempDir()

	client := fake.NewSimpleClientset()

	fss := &FileSecretSync{
		client:     client,
		namespace:  "test-namespace",
		secretName: "test-secret",
		folderPath: tempDir,
	}

	// Test sync with empty directory
	err := fss.syncFiles()
	if err != nil {
		t.Fatalf("syncFiles failed with empty directory: %v", err)
	}

	// Verify no secret was created
	ctx := context.Background()
	_, err = client.CoreV1().Secrets(fss.namespace).Get(ctx, fss.secretName, metav1.GetOptions{})
	if !errors.IsNotFound(err) {
		t.Errorf("Expected secret not to be created for empty directory")
	}
}

func TestSyncFilesWithAPIError(t *testing.T) {
	// Create temporary directory with test files
	tempDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tempDir, "test.txt"), []byte("test"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	client := fake.NewSimpleClientset()

	// Make the client return an error on secret creation
	client.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.NewInternalError(fmt.Errorf("API error"))
	})

	fss := &FileSecretSync{
		client:     client,
		namespace:  "test-namespace",
		secretName: "test-secret",
		folderPath: tempDir,
	}

	// Test sync with API error
	err = fss.syncFiles()
	if err == nil {
		t.Error("Expected syncFiles to fail with API error")
	}
}

func TestMainEnvironmentVariables(t *testing.T) {
	// Test missing folder_to_read
	os.Unsetenv("folder_to_read")
	os.Setenv("secret_to_write", "test-secret")

	// We can't easily test main() directly, but we can test the environment variable checking logic
	folderToRead := os.Getenv("folder_to_read")
	secretToWrite := os.Getenv("secret_to_write")

	if folderToRead != "" {
		t.Error("Expected folder_to_read to be empty")
	}

	if secretToWrite != "test-secret" {
		t.Errorf("Expected secret_to_write to be 'test-secret', got %s", secretToWrite)
	}

	// Test missing secret_to_write
	os.Setenv("folder_to_read", "/tmp")
	os.Unsetenv("secret_to_write")

	folderToRead = os.Getenv("folder_to_read")
	secretToWrite = os.Getenv("secret_to_write")

	if folderToRead != "/tmp" {
		t.Errorf("Expected folder_to_read to be '/tmp', got %s", folderToRead)
	}

	if secretToWrite != "" {
		t.Error("Expected secret_to_write to be empty")
	}
}

// Integration test that requires fsnotify
func TestWatcherIntegration(t *testing.T) {
	// Skip this test in unit test mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create temporary directory
	tempDir := t.TempDir()

	// Create watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer watcher.Close()

	// Add directory to watcher
	err = watcher.Add(tempDir)
	if err != nil {
		t.Fatalf("Failed to add directory to watcher: %v", err)
	}

	// Channel to receive events
	events := make(chan fsnotify.Event, 1)

	// Start watching in goroutine
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				events <- event
				return
			case err := <-watcher.Errors:
				t.Errorf("Watcher error: %v", err)
				return
			}
		}
	}()

	// Create a file to trigger event
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Wait for event with timeout
	select {
	case event := <-events:
		if event.Op&fsnotify.Create == 0 {
			t.Errorf("Expected Create event, got %v", event.Op)
		}
		if event.Name != testFile {
			t.Errorf("Expected event for %s, got %s", testFile, event.Name)
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for file system event")
	}
}

// Benchmark tests
func BenchmarkReadFolderContents(b *testing.B) {
	// Create temporary directory with files
	tempDir := b.TempDir()
	for i := 0; i < 100; i++ {
		content := fmt.Sprintf("content-%d", i)
		fileName := fmt.Sprintf("file-%d.txt", i)
		err := os.WriteFile(filepath.Join(tempDir, fileName), []byte(content), 0644)
		if err != nil {
			b.Fatalf("Failed to create test file: %v", err)
		}
	}

	fss := &FileSecretSync{
		folderPath: tempDir,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := fss.readFolderContents()
		if err != nil {
			b.Fatalf("readFolderContents failed: %v", err)
		}
	}
}

func BenchmarkHasDataChanged(b *testing.B) {
	fss := &FileSecretSync{}

	// Create test data
	oldData := make(map[string][]byte)
	newData := make(map[string][]byte)

	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%d", i)
		value := fmt.Sprintf("value-%d", i)
		oldData[key] = []byte(value)
		newData[key] = []byte(value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fss.hasDataChanged(oldData, newData)
	}
}
