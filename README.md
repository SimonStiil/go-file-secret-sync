# go-file-secret-sync

**go-file-secret-sync** is a lightweight Go application designed to securely synchronize secrets from a file mounted in a Kubernetes pod (such as those written by a sidecar container) and create or update a Kubernetes Secret resource with the file contents. This enables seamless and automated secret propagation from sidecars (like vault agents, external secret injectors, or other tools) into native Kubernetes secrets for use by other workloads.

## Features

- Watches a specific file (or directory) for changes inside a pod.
- Reads and propagates the secret content to a Kubernetes Secret resource.
- Designed to be co-located with a sidecar that writes or refreshes the secret file.
- Minimal dependencies, easy to configure and deploy.

## Use Case

This tool is typically run as a container in the same pod as a sidecar (e.g., Vault Agent, custom secret injector) that writes secrets to a shared volume. When the file changes, **go-file-secret-sync** reads the new value and updates a Kubernetes Secret in the cluster.

## Example Deployment

An example deployment can be found in [`deployment/`](deployment/) using a "fake" sidecar container named `your-sidecar-container`. This sidecar simulates the process of writing a secret to a file that `go-file-secret-sync` will pick up.

## How It Works

1. **Sidecar writes secret**: The sidecar container (`your-sidecar-container`) writes a secret value to `FOLDER_TO_READ` in a shared `emptyDir` volume.
2. **Sync container reads and propagates**: The `go-file-secret-sync` container watches `FOLDER_TO_READ`. When it detects a change, it reads the contents and updates (or creates) a Kubernetes Secret (`SECRET_TO_WRITE`) in the current namespace.

## Configuration

The tool is configured via environment variables:

| Variable         | Description                                                                                   | Required | Example                |
|------------------|----------------------------------------------------------------------------------------------|----------|------------------------|
| `FOLDER_TO_READ` | Path to the file to watch/read.                                                              | Yes      | `/home/user/my-credentials`   |
| `SECRET_TO_WRITE`| Name of the Kubernetes Secret to create/update.                                              | Yes      | `go-file-secret-sync`     |

## Building

```bash
go build -o go-file-secret-sync .
```

## Security Considerations

- **Credentials**: Ensure the container has access to a Kubernetes ServiceAccount with sufficient permissions to create or update secrets in the desired namespace.
- **Secret Management**: Use this tool only for secrets that are safe to propagate to Kubernetes Secrets; consider RBAC and PodSecurity policies.

## License

MIT License

---
