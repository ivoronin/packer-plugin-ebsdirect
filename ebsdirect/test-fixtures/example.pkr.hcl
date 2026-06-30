packer {
  required_plugins {
    ebsdirect = {
      version = ">= 0.1.0"
      source  = "github.com/ivoronin/ebsdirect"
    }
  }
}

# The file builder stands in for whatever produced the raw image (qemu, mkosi,
# dd, ...). Point source at a raw disk whose size is a whole number of GiB.
source "file" "image" {
  source = "disk.raw"
  target = "disk.raw"
}

build {
  sources = ["source.file.image"]

  post-processor "ebsdirect" {
    ami_name      = "my-image"
    architecture  = "x86_64"
    boot_mode     = "legacy-bios"
    tags          = { project = "demo" }
    snapshot_tags = { project = "demo" }
  }
}
