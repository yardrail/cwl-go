cwlVersion: v1.2
class: Workflow
inputs:
  message: string
outputs: []
steps:
  wrong:
    run: notes.txt
    in:
      message: message
    out: []
