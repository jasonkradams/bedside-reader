# List all available recipes
default:
    @just --list

# Stage boot configuration files (config.txt, cmdline.txt) to the Pi boot partition
stage-boot boot_dir="/Volumes/bootfs":
    @if [ ! -d "{{boot_dir}}" ]; then \
        echo "Error: Directory '{{boot_dir}}' does not exist." >&2; \
        exit 1; \
    fi
    cp -v boot/config.txt "{{boot_dir}}/config.txt"
    cp -v boot/cmdline.txt "{{boot_dir}}/cmdline.txt"
