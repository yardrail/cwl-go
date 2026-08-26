class: CommandLineTool
baseCommand: [echo, "hi"]
inputs: []
outputs:
  out:
    type: File
    outputBinding:
      glob: out.txt
stdout: out.txt
