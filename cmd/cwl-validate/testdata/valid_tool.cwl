cwlVersion: v1.2
class: CommandLineTool
id: "#echo"
label: echo a message
baseCommand: echo
inputs:
  message:
    type: string
    inputBinding:
      position: 1
outputs:
  out:
    type: stdout
