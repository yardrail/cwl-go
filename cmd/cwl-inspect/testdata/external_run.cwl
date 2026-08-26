cwlVersion: v1.2
class: Workflow
id: "#outer"
inputs:
  message: string
outputs:
  result:
    type: File
    outputSource: step_one/report
steps:
  step_one:
    run: tool.cwl
    in:
      message: message
    out: [report]
