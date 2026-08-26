cwlVersion: v1.2
class: Workflow
requirements:
  SubworkflowFeatureRequirement: {}
inputs:
  message: string
outputs:
  direct:
    type: File
    outputSource: one/out
  nested:
    type: File
    outputSource: two/out
steps:
  one:
    run: tool.cwl
    in:
      message: message
    out: [out]
  two:
    run: nested/sub.cwl
    in:
      message: message
    out: [out]
