cwlVersion: v1.2
class: CommandLineTool
id: "#schemadef"
baseCommand: [report]
requirements:
  - class: SchemaDefRequirement
    types:
      - name: coordinate
        type: record
        fields:
          - name: chromosome
            type: string
            inputBinding:
              prefix: "--chr"
          - name: position
            type: int
            inputBinding:
              prefix: "--pos"
      - name: region
        type: record
        fields:
          - name: from
            type: coordinate
            inputBinding:
              position: 1
          - name: to
            type: "#coordinate"
            inputBinding:
              position: 2
      - name: strand
        type: enum
        symbols: [plus, minus]
inputs:
  - id: target
    type: region
    inputBinding:
      prefix: "--target"
  - id: many
    type:
      type: array
      items: coordinate
  - id: maybe
    type: ["null", strand]
  - id: plain
    type: string
outputs: []
