cwlVersion: v1.2
class: Workflow
requirements:
  SubworkflowFeatureRequirement: {}
inputs:
  message: string
outputs: []
steps:
  outer:
    run: chain_missing.cwl
    in:
      message: message
    out: []
