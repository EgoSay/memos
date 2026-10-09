import { useMemo } from "react";
import JournalCalendar from "@/components/JournalCalendar";
import MemoEditor from "@/components/MemoEditor";
import { deriveDefaultCreateTimeFromFilters } from "@/components/MemoEditor/utils/deriveDefaultCreateTime";
import MemoView from "@/components/MemoView";
import PagedMemoList, { getMemoKey } from "@/components/PagedMemoList";
import { useAuth } from "@/contexts/AuthContext";
import { useGlobalMemoEditor } from "@/contexts/GlobalMemoEditorContext";
import { useMemoFilterContext } from "@/contexts/MemoFilterContext";
import { NewMemoProvider } from "@/contexts/NewMemoContext";
import { useSpaceContext } from "@/contexts/SpaceContext";
import { useMemoFilters, useMemoSorting } from "@/hooks";
import useCurrentUser from "@/hooks/useCurrentUser";
import useMediaQuery from "@/hooks/useMediaQuery";
import { spaceScopedCacheKey } from "@/lib/resource-names";
import { State } from "@/types/proto/api/v1/common_pb";
import { Memo } from "@/types/proto/api/v1/memo_service_pb";

const Home = () => {
  const user = useCurrentUser();
  const desktop = useMediaQuery("md");
  const { isUserSettingsInitialized } = useAuth();
  const { claimHomeAutoFocus } = useGlobalMemoEditor();
  const { filters } = useMemoFilterContext();
  const { memoFilter: contextFilter, selectedSpaceName, creatorUsername } = useSpaceContext();
  const defaultCreateTime = useMemo(() => deriveDefaultCreateTimeFromFilters(filters), [filters]);
  // Doubles as the remount key: the draft cache only reloads on mount, so the editor
  // has to be rebuilt for the new Space rather than just re-pointed at another cache.
  const editorCacheKey = spaceScopedCacheKey("home-memo-editor", selectedSpaceName);

  const memoFilter = useMemoFilters({
    includeMemoViews: true,
    includePinned: true,
  });
  const canComposeInScope = Boolean(user && (!creatorUsername || creatorUsername === user.username));
  // Pinning is the owner's arrangement of their own memos, so it only shapes a single
  // creator's feed; a mixed-author feed like Explore stays purely chronological.
  const honorPinned = Boolean(creatorUsername);

  const { listSort, orderBy } = useMemoSorting({
    pinnedFirst: honorPinned,
    state: State.NORMAL,
  });

  return (
    <div className="w-full min-h-full bg-background text-foreground">
      <NewMemoProvider>
        <PagedMemoList
          renderer={(memo: Memo, { compact }) => (
            <MemoView
              key={getMemoKey(memo)}
              memo={memo}
              showCreator={!creatorUsername}
              showVisibility
              showPinned={honorPinned}
              showSpace={!selectedSpaceName}
              compact={compact}
              privateDiary={Boolean(user && canComposeInScope)}
            />
          )}
          listSort={listSort}
          orderBy={orderBy}
          filter={memoFilter}
          contextFilter={contextFilter}
          emptyMessage="记录留在这里，什么时候想写都可以。"
          renderHeader={() =>
            user && canComposeInScope ? (
              <header className="mb-6 px-1 pt-3">
                <h1 className="font-serif text-2xl font-medium tracking-tight">记录</h1>
                <p className="mt-2 text-sm text-muted-foreground">给此刻，留一点位置。</p>
              </header>
            ) : null
          }
          renderLeading={({ useGrid }) => {
            if (!isUserSettingsInitialized || !canComposeInScope) return null;

            return (
              <>
                <MemoEditor
                  key={editorCacheKey}
                  autoFocus={desktop ? claimHomeAutoFocus : false}
                  className={useGrid ? undefined : "mb-2"}
                  cacheKey={editorCacheKey}
                  placeholder="写点什么……"
                  defaultCreateTime={defaultCreateTime}
                  defaultSpace={selectedSpaceName}
                />
                {!desktop && <JournalCalendar mobile />}
              </>
            );
          }}
        />
      </NewMemoProvider>
    </div>
  );
};

export default Home;
