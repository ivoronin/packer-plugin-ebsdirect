# For full specification on the configuration of this file visit:
# https://github.com/hashicorp/integration-template#metadata-configuration
integration {
  name = "EBS Direct"
  description = "Registers a raw disk image as an AMI through the EBS direct APIs, with no S3 bucket and no vmimport role."
  identifier = "packer/ivoronin/ebsdirect"
  docs {
    process_docs = true
    readme_location = "./README.md"
    external_url = "https://github.com/ivoronin/packer-plugin-ebsdirect"
  }
  component {
    type = "post-processor"
    name = "EBS Direct"
    slug = "ebsdirect"
  }
}
