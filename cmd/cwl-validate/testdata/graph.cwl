cwlVersion: v1.2
$graph:
  - id: tool
    class: CommandLineTool
    baseCommand: echo
    inputs:
      message:
        type: string
        inputBinding: {position: 1}
    outputs:
      out:
        type: stdout
  - id: main
    class: Workflow
    inputs:
      message: string
    outputs:
      result:
        type: File
        outputSource: step_one/out
    steps:
      step_one:
        run: "#tool"
        in:
          message: message
        out: [out]
