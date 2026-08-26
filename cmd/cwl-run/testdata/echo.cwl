cwlVersion: v1.2
class: CommandLineTool
baseCommand: echo
inputs:
  message:
    type: string
    default: hello
    inputBinding:
      position: 1
stdout: out.txt
outputs:
  out:
    type: File
    outputBinding:
      glob: out.txt
