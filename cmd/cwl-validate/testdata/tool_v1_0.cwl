cwlVersion: v1.0
class: CommandLineTool
id: "#echo-v10"
baseCommand: echo
inputs:
  message:
    type: File
    secondaryFiles:
      - ".2"
outputs:
  out:
    type: stdout
