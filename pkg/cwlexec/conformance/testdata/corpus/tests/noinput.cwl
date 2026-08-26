cwlVersion: v1.2
class: CommandLineTool
baseCommand: [echo, "no job"]
inputs: []
outputs:
  out:
    type: File
    outputBinding:
      glob: out.txt
stdout: out.txt
