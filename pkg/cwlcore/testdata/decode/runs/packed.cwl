cwlVersion: v1.2
$graph:
  - id: "#helper"
    class: CommandLineTool
    baseCommand: [cat]
    stdout: helper.txt
    inputs:
      message: string
    outputs:
      out: stdout
  - id: "#main"
    class: Workflow
    inputs:
      message: string
    outputs:
      out:
        type: File
        outputSource: run_helper/out
    steps:
      run_helper:
        run: "#helper"
        in:
          message: message
        out: [out]
