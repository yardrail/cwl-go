cwlVersion: v1.2
class: Workflow
requirements:
  SubworkflowFeatureRequirement: {}
inputs:
  message: string
outputs: []
steps:
  to_a:
    run: cycle_a.cwl
    in:
      message: message
    out: []
