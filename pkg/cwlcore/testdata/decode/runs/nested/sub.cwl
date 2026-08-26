cwlVersion: v1.2
class: Workflow
inputs:
  message: string
outputs:
  out:
    type: File
    outputSource: inner/out
steps:
  inner:
    run: ../tool.cwl
    in:
      message: message
    out: [out]
