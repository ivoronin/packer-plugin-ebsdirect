The ebsdirect plugin registers a raw disk image as an AMI through the EBS direct APIs,
with no S3 bucket and no vmimport role.

### Installation

To install this plugin, copy and paste this code into your Packer configuration, then
run [`packer init`](https://www.packer.io/docs/commands/init).

```hcl
packer {
  required_plugins {
    ebsdirect = {
      source  = "github.com/ivoronin/ebsdirect"
      version = "~> 0.1"
    }
  }
}
```

Alternatively, you can use `packer plugins install` to manage installation of this plugin.

```sh
$ packer plugins install github.com/ivoronin/ebsdirect
```

### Components

#### Post-processors

- [ebsdirect](/packer/integrations/ivoronin/ebsdirect/latest/components/post-processor/ebsdirect) - Registers a raw disk image as an AMI through the EBS direct APIs.

### Example Usage

```hcl
source "file" "image" {
  source = "disk.raw"
  target = "disk.raw"
}

build {
  sources = ["source.file.image"]

  post-processor "ebsdirect" {
    ami_name = "my-image"
  }
}
```
