import { Card, Icon, PageHeader, StatusBadge } from "../../components/primitives";
import { Button } from "../../components/ui/button";
import { useApp, usePageTitle } from "../../runtime/app-context";
import type { PageProps } from "../../runtime/types";
import { DOCTOR_STATUSES, doctorApplyDetail, doctorPlain, type DoctorCheck } from "./report";
import { useDoctor } from "./use-doctor";

function CheckRow({ check, labels }: { check: DoctorCheck; labels: Record<string, string> }) {
  const { can, navigate } = useApp();
  const status = DOCTOR_STATUSES.find(item => item.status === check.status);
  const nodeName = /^节点 (.+)$/.exec(check.name)?.[1];
  const label = nodeName ? labels[nodeName] || nodeName : "";
  const hint = doctorPlain(check.ui_hint || check.hint);
  const shown = nodeName && label !== nodeName ? hint.split(`打开 ${nodeName}`).join(`打开 ${label}`) : hint;
  const apply = check.ui_action === "apply" && !!check.node_id;
  const showApply = apply && can("POST", "/api/nodes/x");
  const detail = apply ? doctorApplyDetail(check.detail) : check.detail;
  const advice = showApply && shown.includes("应用到节点") ? "" : shown;

  return <tr data-check={check.name}>
    <td><b className="bold">{nodeName ? "节点 " + label : doctorPlain(check.name)}</b></td>
    <td><StatusBadge tone={status?.tone}>{status?.label || check.status}</StatusBadge></td>
    <td className="doc-d">
      <div>{detail}{showApply && <Button size="sm" data-act="doc-apply" data-id={check.node_id}
        onClick={() => navigate("nodes/" + check.node_id, { apply: "1" })}>应用到{label || nodeName}</Button>}</div>
      {advice && <div className="sub">建议：{advice}</div>}
    </td>
  </tr>;
}

export function DoctorPage({ route: _route }: PageProps) {
  usePageTitle([{ label: "体检" }]);
  const { report, error, labels, running, run } = useDoctor();
  const checks = report?.checks || [];

  return <>
    <PageHeader title="体检" description="控制面、每台节点、六种订阅格式、采样表" actions={
      <Button variant="primary" loading={running} data-act={running ? undefined : "doc-run"} onClick={() => void run()}>
        {!running && <Icon name="check" />}开始体检
      </Button>
    } />
    {running ? <Card className="pad16">
      <div className="callout stone"><Icon name="refresh" /><div>正在体检：会逐台连上节点现场查一次，可能要几十秒。只读不改，关掉这个页面就不查了，回来再点一次就行。</div></div>
    </Card> : error ? <Card className="pad16">
      <div className="callout bad" role="alert"><Icon name="warn" className="ico-bad" /><div>
        <b>体检没有跑成</b><div className="mono mt2" data-doc-err="">{error}</div>
      </div></div>
      <p className="sub mt">控制面原话在上面。处理完再点一次「开始体检」。</p>
    </Card> : !report ? <Card className="pad16">
      <b className="b6">一次体检，只读不改</b>
      <p className="sub mt6 mb14">检查控制面的库能不能读、后台任务还在不在跳;每一台节点现场问一次(服务在不在跑、配置有没有漂移、证书还剩几天);六种订阅格式各渲染一次;采样表有多少行。体检只报告，不动任何东西 —— 发现的问题要不要处置由你决定。</p>
      <p className="sub mb8">它会对每台节点 SSH 一次，<b className="c-ink">要几十秒到几分钟</b>，所以进页面不自动跑。点右上角「开始体检」即可。</p>
    </Card> : <>
      <div className="grid g3">{DOCTOR_STATUSES.map(status => {
        const count = checks.filter(check => check.status === status.status).length;
        return <Card className="stat" key={status.status}>
          <div className="l"><span className={"dot " + status.tone} />{status.label}</div>
          <div className={"v" + (status.status === "fail" && count > 0 ? " c-bad" : "")} data-sum={status.status}>{count}</div>
          <div className="s">{status.description}</div>
        </Card>;
      })}</div>
      <Card className="mt">
        <div className="ch">检查项<small>· 共 {checks.length} 项</small></div>
        {checks.length ? <div className="tw"><table>
          <thead><tr><th scope="col">名称</th><th scope="col">结论</th><th scope="col">说明</th></tr></thead>
          <tbody>{checks.map((check, index) => <CheckRow key={check.name + ":" + index} check={check} labels={labels} />)}</tbody>
        </table></div> : <div className="row sub">控制面没有给出任何检查项。</div>}
      </Card>
    </>}
  </>;
}
