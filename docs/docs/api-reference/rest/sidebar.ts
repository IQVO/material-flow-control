import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/material-flow-control",
    },
    {
      type: "category",
      label: "transport-requests",
      link: {
        type: "doc",
        id: "api-reference/rest/transport-requests",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/request-transport",
          label: "Request a transport",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-transport",
          label: "Read a transport request",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-transport",
          label: "Cancel a transport",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "container-tracks",
      link: {
        type: "doc",
        id: "api-reference/rest/container-tracks",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-container-track",
          label: "Read a container's track",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/resolve-clarification",
          label: "Resolve a container's clarification",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "stations",
      link: {
        type: "doc",
        id: "api-reference/rest/stations",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-station",
          label: "Read a station queue",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "subsystems",
      link: {
        type: "doc",
        id: "api-reference/rest/subsystems",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/list-subsystems",
          label: "List the subsystem capacity snapshots",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "probes",
      link: {
        type: "doc",
        id: "api-reference/rest/probes",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/healthz",
          label: "Liveness",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/readyz",
          label: "Readiness",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
