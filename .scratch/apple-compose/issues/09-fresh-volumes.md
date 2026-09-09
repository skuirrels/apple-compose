# 09 Fresh volumes and database images
Type: research
Status: resolved

## Question
Why does postgres fail to initialise on a brand-new named volume?

## Answer
Runtime volumes are ext4 images whose root holds `lost+found`; initdb refuses a non-empty data directory ("It contains a lost+found directory, perhaps due to it being a mount point"). Docker volumes start empty. apple-compose now pulls images before creating volumes and, right after `volume create`, runs the first image that mounts the volume with `--entrypoint rmdir` on `lost+found` (no network, no DNS, auto-removed). Images without rmdir produce a warning. Verified with postgres:16-alpine reaching healthy on a fresh volume.
