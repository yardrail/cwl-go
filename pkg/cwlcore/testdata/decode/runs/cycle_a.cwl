cwlVersion: v1.2
class: Workflow
requirements:
  SubworkflowFeatureRequirement: {}
inputs:
  message: string
outputs: []
steps:
  to_b:
    run: cycle_b.cwl
    in:
      message: message
    out: []
