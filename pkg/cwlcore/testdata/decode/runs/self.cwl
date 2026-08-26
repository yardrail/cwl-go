cwlVersion: v1.2
class: Workflow
requirements:
  SubworkflowFeatureRequirement: {}
inputs:
  message: string
outputs: []
steps:
  again:
    run: self.cwl
    in:
      message: message
    out: []
