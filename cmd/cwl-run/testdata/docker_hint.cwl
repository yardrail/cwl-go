cwlVersion: v1.2
class: CommandLineTool
hints:
  DockerRequirement:
    dockerPull: docker.io/library/alpine:latest
baseCommand: echo
arguments: [hinted]
stdout: out.txt
inputs: []
outputs:
  out:
    type: File
    outputBinding:
      glob: out.txt
