cwlVersion: v1.2
class: Workflow
requirements:
  SubworkflowFeatureRequirement: {}
inputs: []
outputs:
  result:
    type: File
    outputSource: nest/out
steps:
  nest:
    run: subworkflow_child.cwl
    in: []
    out: [out]
