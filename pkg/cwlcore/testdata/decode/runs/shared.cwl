cwlVersion: v1.2
class: Workflow
inputs:
  message: string
outputs: []
steps:
  first:
    run: tool.cwl
    in:
      message: message
    out: []
  second:
    run: tool.cwl
    in:
      message: message
    out: []
