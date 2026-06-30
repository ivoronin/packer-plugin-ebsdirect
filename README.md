# packer-plugin-ebsdirect

Register a raw disk image as an AMI through the EBS direct APIs - no S3 bucket and no `vmimport` role.

[![test](https://github.com/ivoronin/packer-plugin-ebsdirect/actions/workflows/test.yml/badge.svg)](https://github.com/ivoronin/packer-plugin-ebsdirect/actions/workflows/test.yml)
[![release](https://img.shields.io/github/v/release/ivoronin/packer-plugin-ebsdirect)](https://github.com/ivoronin/packer-plugin-ebsdirect/releases)

## Table of Contents

[Overview](#overview) · [Features](#features) · [Installation](#installation) · [Usage](#usage) · [Configuration](#configuration) · [Requirements](#requirements) · [License](#license)

```hcl
# amazon-import uploads through S3 and needs the vmimport service role:
#   post-processor "amazon-import" {
#     s3_bucket_name = "my-vmimport-bucket"   # create it, manage it, pay for it
#     role_name      = "vmimport"             # set up its trust policy first
#     ...
#   }

# ebsdirect writes the snapshot straight from the raw image:
post-processor "ebsdirect" {
  ami_name = "my-image"
}
```

## Overview

`ebsdirect` is a Packer post-processor that takes a raw disk image from a previous build step and turns it into an AMI without going through S3. It uploads the image straight into an EBS snapshot with the EBS direct APIs (`StartSnapshot` / `PutSnapshotBlock` / `CompleteSnapshot`), writing 512 KiB blocks in parallel and skipping all-zero ones, then registers that snapshot as an AMI with `RegisterImage`. Credentials come from the default AWS SDK chain.

This drops the two things `amazon-import` requires that have nothing to do with the image itself: the S3 bucket you upload through, and the `vmimport` IAM role with its trust policy.

Available on the [Packer integrations registry](https://developer.hashicorp.com/packer/integrations/ivoronin/ebsdirect).

## Features

- Uploads via the EBS direct APIs - no S3 bucket and no `vmimport` role to set up.
- Skips all-zero 512 KiB blocks, so a 10 GiB image holding 2 GiB of data uploads about 2 GiB.
- Parallel block upload (64 workers) with retry on throttling.
- Per-block SHA-256 plus a LINEAR aggregate checksum that AWS validates at `CompleteSnapshot`.
- Registers a modern HVM AMI: ENA enabled, gp3 root volume (16 TiB ceiling), `x86_64` or `arm64`, boot mode `legacy-bios` / `uefi` / `uefi-preferred`.
- Optional snapshot encryption (`ami_encrypt` / `ami_kms_key`) with the account default or a customer-managed KMS key.

## Installation

Declare the plugin in your template and run `packer init`:

```hcl
packer {
  required_plugins {
    ebsdirect = {
      version = ">= 0.1.0"
      source  = "github.com/ivoronin/ebsdirect"
    }
  }
}
```

```bash
packer init .
```

Prebuilt binaries are on the [releases page](https://github.com/ivoronin/packer-plugin-ebsdirect/releases).

## Usage

Chain the post-processor after any builder that produces a raw image. With the core `file` builder pointing at an image you built elsewhere:

```hcl
source "file" "image" {
  source = "disk.raw"   # raw format, a whole number of GiB
  target = "disk.raw"
}

build {
  sources = ["source.file.image"]

  post-processor "ebsdirect" {
    ami_name      = "my-image"
    architecture  = "x86_64"        # or arm64
    boot_mode     = "legacy-bios"   # or uefi / uefi-preferred
    tags          = { project = "demo" }
    snapshot_tags = { project = "demo" }
  }
}
```

The same shape works after a `qemu` build that outputs a raw disk. The post-processor produces an AMI artifact with id `<region>:<ami-id>`; destroying that artifact deregisters the AMI and deletes its snapshot.

## Configuration

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `ami_name` | yes | - | Name of the registered AMI. |
| `ami_description` | no | `""` | AMI description. |
| `architecture` | no | `x86_64` | `x86_64` or `arm64` (arm64 requires `uefi`). |
| `boot_mode` | no | `legacy-bios` | `legacy-bios`, `uefi`, or `uefi-preferred`. |
| `root_device_name` | no | `/dev/xvda` | Root device name on the AMI. |
| `region` | no | from the SDK chain | Target region; otherwise `AWS_REGION` or the active profile. |
| `tags` | no | - | Tags applied to the AMI. |
| `snapshot_tags` | no | - | Tags applied to the snapshot. |
| `ami_encrypt` | no | `false` | Request encryption of the snapshot (and the resulting AMI). An account/region with EBS encryption-by-default still produces an encrypted snapshot regardless. |
| `ami_kms_key` | no | account default EBS key | KMS key ARN for the encrypted snapshot. Requires `ami_encrypt = true`; ignored otherwise. |

Credentials are read from the default AWS SDK chain (environment, `AWS_PROFILE` / shared config, SSO, instance role). There are no credential fields in the template.

## Requirements

- Packer 1.7 or later (for `packer init`).
- A raw disk image whose size is a whole number of GiB. Use `qemu-img convert -O raw` to convert and `qemu-img resize` to round up if needed.
- IAM permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["ebs:StartSnapshot", "ebs:PutSnapshotBlock", "ebs:CompleteSnapshot"],
      "Resource": "arn:aws:ec2:*::snapshot/*"
    },
    {
      "Effect": "Allow",
      "Action": ["ec2:RegisterImage", "ec2:DescribeSnapshots", "ec2:DeregisterImage", "ec2:DeleteSnapshot", "ec2:CreateTags"],
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": ["kms:DescribeKey", "kms:GenerateDataKeyWithoutPlaintext", "kms:CreateGrant", "kms:ReEncrypt*", "kms:Decrypt"],
      "Resource": "*"
    }
  ]
}
```

The KMS actions are only needed when `ami_encrypt` is used with a customer-managed key; the account default EBS key is already usable by account principals.

## License

[MPL-2.0](LICENSE)
