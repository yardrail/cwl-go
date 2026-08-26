cwlVersion: v1.2
class: Workflow
inputs:
  message:
    type: string
    default: from a workflow
outputs:
  result:
    type: File
    outputSource: shout/out
steps:
  shout:
    run: echo.cwl
    in:
      message: message
    out: [out]
