cwlVersion: v1.2
class: Workflow
id: "#main"
label: two step workflow
requirements:
  - class: ResourceRequirement
    coresMin: 4
hints:
  - class: DockerRequirement
    dockerPull: "alpine:3"
inputs:
  message: string
outputs:
  result:
    type: File
    outputSource: step_two/out
steps:
  step_one:
    in:
      message: message
    out: [out]
    requirements:
      - class: ToolTimeLimit
        timelimit: 60
    run:
      class: CommandLineTool
      cwlVersion: v1.2
      baseCommand: echo
      inputs:
        message:
          type: string
          inputBinding: {position: 1}
      outputs:
        out:
          type: stdout
  step_two:
    in:
      x: step_one/out
    out: [out]
    run:
      class: ExpressionTool
      cwlVersion: v1.2
      expression: "${return {out: inputs.x};}"
      inputs:
        x: File
      outputs:
        out: File
