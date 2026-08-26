cwlVersion: v1.2
class: "ex:CustomTool"
id: "#custom"
label: an extension process
customField:
  knob: 11
requirements:
  - class: "ex:CustomRequirement"
    strictness: high
hints:
  - class: "ex:CustomHint"
    note: advisory
inputs:
  prompt: string
outputs:
  answer: string
