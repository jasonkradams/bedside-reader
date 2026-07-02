# List all available recipes
_default:
    @just --list


# Stage boot configuration files (config.txt, cmdline.txt) to the Pi boot partition
stage-boot boot_dir="/Volumes/bootfs":
    @if [ ! -d "{{boot_dir}}" ]; then \
        echo "Error: Directory '{{boot_dir}}' does not exist." >&2; \
        exit 1; \
    fi
    cp -v boot/config.txt "{{boot_dir}}/config.txt"
    cp -v boot/cmdline.txt "{{boot_dir}}/cmdline.txt"
    cp -v boot/user-data "{{boot_dir}}/user-data"
    cp -v boot/panel.bin "{{boot_dir}}/panel.bin"

# Build the bedside-reader Go binary and deploy it to the Pi
deploy host="10.136.117.83" user="pi":
    @echo "Building for linux/arm64..."
    GOOS=linux GOARCH=arm64 go build -o build/bedside ./cmd/bedside
    @echo "Deploying to {{user}}@{{host}}..."
    @ssh -o StrictHostKeyChecking=no "{{user}}@{{host}}" "sudo systemctl stop bedside.service || true"
    @scp -o StrictHostKeyChecking=no build/bedside "{{user}}@{{host}}:/tmp/bedside"
    @ssh -o StrictHostKeyChecking=no "{{user}}@{{host}}" "sudo mv /tmp/bedside /usr/local/bin/bedside && sudo chmod +x /usr/local/bin/bedside && sudo systemctl start bedside.service"
    @echo "Deployment complete! Service bedside.service restarted."
