cwlVersion: v1.2
class: CommandLineTool
baseCommand: [echo]
stdout: out.txt
inputs:
  message: string
outputs:
  out: stdout
