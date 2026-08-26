cwlVersion: v1.2
class: Workflow
inputs:
  message: string
outputs:
  out:
    type: File
    outputSource: helper/out
steps:
  helper:
    run: packed.cwl#helper
    in:
      message: message
    out: [out]
