cwlVersion: v1.2
class: Workflow
requirements:
  SubworkflowFeatureRequirement: {}
inputs:
  message: string
outputs: []
steps:
  relay:
    run: missing.cwl
    in:
      message: message
    out: []
