cwlVersion: v1.2
class: Workflow
inputs:
  message: string
outputs: []
steps:
  gone:
    run: absent-tool.cwl
    in:
      message: message
    out: []
