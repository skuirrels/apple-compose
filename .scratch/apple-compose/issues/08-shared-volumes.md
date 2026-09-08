# 08 Named volumes mounted by several services
Type: research
Status: resolved

## Question
What happens when two services mount the same named volume, a common Compose pattern?

## Answer
The runtime's named volumes are ext4 disk images attached to the VM as block devices; starting a second container with the same volume fails with `VZErrorDomain Code=2 "The storage device attachment is invalid"` (verified on 1.3.1). apple-compose warns at `up` when a volume is mounted by more than one container, adds a hint to that runtime error, and offers host-directory backing: Docker's `driver_opts: {type: none, o: bind, device: ./path}` or the extension `x-apple-compose: {shared: true}`, which keeps the directory under the state directory and removes it on `down -v`.
