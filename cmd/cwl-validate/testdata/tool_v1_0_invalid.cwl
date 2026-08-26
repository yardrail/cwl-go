cwlVersion: v1.0
class: CommandLineTool
id: "#echo-v10-invalid"
baseCommand: echo
inputs:
  message:
    type: File
    secondaryFiles:
      - pattern: ".2"
        required: true
outputs:
  out:
    type: stdout
